package vxlan

import (
	"context"
	"net"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	vxlanMech "github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/vxlan"
	"github.com/networkservicemesh/sdk/pkg/tools/log"

	"github.com/pkg/errors"

	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	kernellink "github.com/networkservicemesh/sdk-kernel/pkg/kernel"
	"github.com/networkservicemesh/sdk-kernel/pkg/kernel/tools/nshandle"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/link"

	"github.com/vishvananda/netlink"
)

func Create(ctx context.Context, conn *networkservice.Connection, outgoing bool) error {
	logger := log.FromContext(ctx).WithField("vxlan", "Intf create")
	if mechanism := vxlanMech.ToMechanism(conn.GetMechanism()); mechanism != nil {
		if mechanism.GetParameters() == nil {
			return errors.Errorf("vxlan parameters not provided")
		}
		if mechanism.SrcIP() == nil {
			return errors.Errorf("vxlan SrcIP not provided")
		}
		if mechanism.DstIP() == nil {
			return errors.Errorf("vxlan DstIP not provided")
		}
		if mechanism.VNI() == 0 {
			return errors.Errorf("vxlan VNI not provided")
		}

		ok := false
		ifaceName := ""
		ifaceName, ok = mechanism.GetParameters()["name"]
		if !ok || ifaceName == "" {
			return errors.Errorf("vxlan interface name not provided")
		}
		netNsUrl := ""
		netNsUrl, ok = mechanism.GetParameters()["inodeURL"]
		if !ok || netNsUrl == "" {
			return errors.Errorf("vxlan inode URL not provided")
		}

		// Resolve local egress and remote IP addresses. If the local forwarder is on the same
		// node as the connection requestor, the outgoing flag would be set, meaning that the
		// connection would be initiated from the local forwarder towards the remote node.
		var egressIP, remoteIP net.IP
		if !outgoing {
			egressIP = mechanism.DstIP()
			remoteIP = mechanism.SrcIP()
		} else {
			remoteIP = mechanism.DstIP()
			egressIP = mechanism.SrcIP()
		}

		vni := mechanism.VNI()

		logger.Infof("netnsurl: %v: iface: %v: srcIP: %s: dstIP: %s: vni: %v", netNsUrl, ifaceName, egressIP.String(), remoteIP.String(), vni)

		// Construct the netlink handle for the target network namespace for this kernel interface
		handle, err := kernellink.GetNetlinkHandle(netNsUrl)
		if err != nil {
			return errors.WithStack(err)
		}
		defer handle.Close()

		// The cache only contains links created by the forwarder. Check the cache for the link.
		// If the link is present, treat the Link Create request as redundant and return.
		if _, ok := link.Load(ctx, outgoing); ok {
			// Check if the link is already present in the target network namespace
			if _, err = handle.LinkByName(ifaceName); err == nil {
				return nil
			}
		}

		// Forwarder is not aware of the link since it is not present in the cache.
		// Delete the previous kernel interface if there is one in the target namespace, it could be a stale/dangling interface.
		var prevLink netlink.Link
		if prevLink, err = handle.LinkByName(ifaceName); err == nil {
			if err = handle.LinkDel(prevLink); err != nil {
				return errors.WithStack(err)
			}
			log.FromContext(ctx).
				WithField("link.Name", prevLink.Attrs().Name).
				WithField("netlink", "LinkDel").Debug("completed")
		}

		// Create the vxlan link in the host network namespace. It will be inserted into the target namespace later on in the func.
		fwdNsIfaceName := getVxlanLinkName(conn.GetId())
		if err := netlink.LinkAdd(newVXLAN(fwdNsIfaceName, egressIP, remoteIP, int(vni))); err != nil {
			return errors.Wrapf(err, "failed to create VXLAN interface")
		}

		log.FromContext(ctx).WithField("link.Name", fwdNsIfaceName).WithField("netlink", "LinkAdd vxlan").Debug("completed")

		l, err := netlink.LinkByName(fwdNsIfaceName)
		if err != nil {
			log.FromContext(ctx).
				WithField("link.Name", fwdNsIfaceName).
				WithField("err", err).
				WithField("netlink", "LinkByName").Debug("error")
			return errors.WithStack(err)
		}

		// Construct the nsHandle for the target namespace for this kernel interface
		nsHandle, err := nshandle.FromURL(mechanism.GetParameters()["inodeURL"])
		if err != nil {
			return errors.WithStack(err)
		}
		defer func() { _ = nsHandle.Close() }()

		// Insert the link in the target namespace
		if err = netlink.LinkSetNsFd(l, int(nsHandle)); err != nil {
			return errors.Wrapf(err, "unable to change to netns")
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("netlink", "LinkSetNsFd").Debug("completed")

		l, err = handle.LinkByName(l.Attrs().Name)
		if err != nil {
			log.FromContext(ctx).
				WithField("link.Name", l.Attrs().Name).
				WithField("err", err).
				WithField("netlink", "LinkByName").Debug("error")
		}

		// Set the LinkName to the name specified in the request. The link in the target namespace
		// would have this name assigned to it.
		if err = handle.LinkSetName(l, ifaceName); err != nil {
			log.FromContext(ctx).
				WithField("link.Name", l.Attrs().Name).
				WithField("link.NewName", ifaceName).
				WithField("err", err).
				WithField("netlink", "LinkSetName").Debug("error")
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("link.NewName", ifaceName).
			WithField("netlink", "LinkSetName").Debug("completed")

		// Set link alias to the name of the network service client pod requesting for this link.
		if conn.GetLabels() != nil {
			linkAlias := conn.GetLabels()["podName"]
			if linkAlias != "" {
				if err = handle.LinkSetAlias(l, linkAlias); err != nil {
					return errors.WithStack(err)
				}
				log.FromContext(ctx).
					WithField("link.Name", l.Attrs().Name).
					WithField("alias", linkAlias).
					WithField("netlink", "LinkSetAlias").Debug("completed")
			}
		}

		// Set the state of the link to UP.
		err = handle.LinkSetUp(l)
		if err != nil {
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("netlink", "LinkSetUp").Debug("completed")

		// Store the link data in the cache
		link.Store(ctx, outgoing, l)
	}

	return nil
}

func Delete(ctx context.Context, conn *networkservice.Connection, outgoing bool) error {
	if mechanism := vxlanMech.ToMechanism(conn.GetMechanism()); mechanism != nil {
		if mechanism.GetParameters() == nil {
			return errors.Errorf("vxlan delete: link parameters not provided")
		}
		ok := false
		ifaceName := ""
		ifaceName, ok = mechanism.GetParameters()["name"]
		if !ok || ifaceName == "" {
			return errors.Errorf("vxlan interface name not provided")
		}
		netNsUrl := ""
		netNsUrl, ok = mechanism.GetParameters()["inodeURL"]
		if !ok || netNsUrl == "" {
			return errors.Errorf("vxlan inode URL not provided")
		}

		// Construct the netlink handle for the target namespace for this kernel interface
		handle, err := kernellink.GetNetlinkHandle(netNsUrl)
		if err != nil {
			return errors.WithStack(err)
		}
		defer handle.Close()

		links, err := handle.LinkList()
		if err != nil {
			log.FromContext(ctx).
				WithField("err", err).
				WithField("netlink", "LinkList").Debug("error")
			return err
		}

		var linkToDel netlink.Link
		for _, link := range links {
			if link.Attrs().Name == ifaceName {
				linkToDel = link
				break
			}
		}

		if linkToDel == nil {
			log.FromContext(ctx).
				WithField("link.Name", ifaceName).
				WithField("netlink", "LinkByName").Debug("NotFound")
			return nil
		}

		err = handle.LinkDel(linkToDel)
		if err != nil {
			log.FromContext(ctx).
				WithField("link.Name", linkToDel.Attrs().Name).
				WithField("err", err).
				WithField("netlink", "LinkDel").Debug("error")
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("link.Name", linkToDel.Attrs().Name).
			WithField("netlink", "LinkDel").Info("completed")
		link.Delete(ctx, outgoing)
	}

	return nil
}

func getVxlanLinkName(connId string) string {
	return linuxIfaceName(connId)
}

func linuxIfaceName(ifaceName string) string {
	if len(ifaceName) <= kernel.LinuxIfMaxLength {
		return ifaceName
	}
	return ifaceName[:kernel.LinuxIfMaxLength]
}

func newVXLAN(ifaceName string, egressIP, remoteIP net.IP, vni int) *netlink.Vxlan {
	/* Populate the VXLAN interface configuration */
	return &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: ifaceName,
		},
		VxlanId: vni,
		Group:   remoteIP,
		SrcAddr: egressIP,
	}
}
