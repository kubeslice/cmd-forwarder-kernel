package veth

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	kernellink "github.com/networkservicemesh/sdk-kernel/pkg/kernel"
        "github.com/networkservicemesh/sdk-kernel/pkg/kernel/tools/nshandle"
	"github.com/networkservicemesh/sdk/pkg/tools/log"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/link"

	"github.com/vishvananda/netlink"
)

func toAlias(conn *networkservice.Connection, isSrc bool) string {
	// Naming is tricky.  We want to name based on either the next or prev connection id depending on whether we
	// are on the client or server side.  Since this chain element is designed for use in a Forwarder,
	// if we are on the client side, we want to name based on the connection id from the NSE that is Next
	// if we are not the client, we want to name for the connection of of the client addressing us, which is Prev
	namingConn := conn.Clone()
	namingConn.Id = namingConn.GetPrevPathSegment().GetId()
	alias := fmt.Sprintf("server-%s", namingConn.GetId())
	if isSrc {
		namingConn.Id = namingConn.GetNextPathSegment().GetId()
		alias = fmt.Sprintf("client-%s", namingConn.GetId())
	}
	return alias
}

func getVethLinkNamePair(conn *networkservice.Connection, isSrc bool) (string, string) {
	if isSrc {
		return linuxIfaceName(conn.GetId()), linuxIfaceName(fmt.Sprintf("peer-%s", conn.GetId()))

	} else {
		return linuxIfaceName(fmt.Sprintf("peer-%s", conn.GetId())), linuxIfaceName(conn.GetId())
	}
}

func Create(ctx context.Context, conn *networkservice.Connection, isSrc bool) error {
	if mechanism := kernel.ToMechanism(conn.GetMechanism()); mechanism != nil {
		log.FromContext(ctx).Infof("veth create: isSrc: %v, mech: %v", isSrc, mechanism)

		// Construct the netlink handle for the target namespace for this kernel interface
		handle, err := kernellink.GetNetlinkHandle(mechanism.GetNetNSURL())
		if err != nil {
			return errors.WithStack(err)
		}
		defer handle.Close()

		// Check if the link is present in the cache. If present, the create request can be ignored.
		if linkCached, ok := link.Load(ctx, isSrc); ok {
			log.FromContext(ctx).Debug("veth create: link found in cache: isSrc: %v, linkCached: %v", isSrc, linkCached)
			if _, err = handle.LinkByName(mechanism.GetInterfaceName()); err == nil {
				return nil
			}
		}

		// Link not in cache. Delete the previous (stale or of unknown origin) kernel interface if there is one in the target namespace.
		var prevLink netlink.Link
		if prevLink, err = handle.LinkByName(mechanism.GetInterfaceName()); err == nil {
			now := time.Now()
			if err = handle.LinkDel(prevLink); err != nil {
				return errors.WithStack(err)
			}
			log.FromContext(ctx).
				WithField("link.Name", prevLink.Attrs().Name).
				WithField("duration", time.Since(now)).
				WithField("netlink", "LinkDel").Debug("completed")
		}

		var l netlink.Link
		// isSrc determines if this request is to the forwarder originated from the network service client pod.
		// Note that both the client and the server endpoint pods are on the same node for veth links.
		// The veth link is created ONLY if the isSrc is set, which means the request is coming from the client. This is
		// needed because both the client and server are co-located on the same node, and we should create the veth intf
		// only once.
		linkName, peerName := getVethLinkNamePair(conn, isSrc)
		if isSrc {
			// Create the veth pair
			now := time.Now()
			veth := &netlink.Veth{
				LinkAttrs: netlink.LinkAttrs{
					Name: linkName,
				},
				PeerName: peerName,
			}
			l = veth
			if addErr := netlink.LinkAdd(l); addErr != nil {
				return addErr
			}
			log.FromContext(ctx).
				WithField("link.Name", l.Attrs().Name).
				WithField("link.PeerName", veth.PeerName).
				WithField("duration", time.Since(now)).
				WithField("netlink", "LinkAdd").Debug("completed")
		}

		now := time.Now()
		l, err = netlink.LinkByName(linkName)
		if err != nil {
			log.FromContext(ctx).
				WithField("duration", time.Since(now)).
				WithField("link.Name", linkName).
				WithField("err", err).
				WithField("netlink", "LinkByName").Debug("error")
			return errors.WithStack(err)
		}

		// Construct the nsHandle for the target namespace for this kernel interface
		nsHandle, err := nshandle.FromURL(mechanism.GetNetNSURL())
		if err != nil {
			return errors.WithStack(err)
		}
		defer func() { _ = nsHandle.Close() }()

		// Set/Insert the link l to the target netns
		now = time.Now()
		if err = netlink.LinkSetNsFd(l, int(nsHandle)); err != nil {
			return errors.Wrapf(err, "unable to change to netns")
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("duration", time.Since(now)).
			WithField("netlink", "LinkSetNsFd").Debug("completed")

		// Get the link l in the new namespace
		now = time.Now()
		name := l.Attrs().Name
		l, err = handle.LinkByName(name)
		if err != nil {
			log.FromContext(ctx).
				WithField("duration", time.Since(now)).
				WithField("link.Name", name).
				WithField("err", err).
				WithField("netlink", "LinkByName").Debug("error")
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("duration", time.Since(now)).
			WithField("link.Name", name).
			WithField("netlink", "LinkByName").Debug("completed")

		name = mechanism.GetInterfaceName()
		// Set the LinkName
		now = time.Now()
		if err = handle.LinkSetName(l, name); err != nil {
			log.FromContext(ctx).
				WithField("link.Name", l.Attrs().Name).
				WithField("link.NewName", name).
				WithField("duration", time.Since(now)).
				WithField("err", err).
				WithField("netlink", "LinkSetName").Debug("error")
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("link.NewName", name).
			WithField("duration", time.Since(now)).
			WithField("netlink", "LinkSetName").Debug("completed")

		// Set link alias if pod name is set in the labels
		if conn.GetLabels() != nil {
			linkAlias := conn.GetLabels()["podName"]
			if linkAlias != "" {
				now = time.Now()
				if err = handle.LinkSetAlias(l, linkAlias); err != nil {
					return errors.WithStack(err)
				}
				log.FromContext(ctx).
					WithField("link.Name", l.Attrs().Name).
					WithField("alias", linkAlias).
					WithField("duration", time.Since(now)).
					WithField("netlink", "LinkSetAlias").Debug("completed")
			}
		}

		// Up the link
		now = time.Now()
		err = handle.LinkSetUp(l)
		if err != nil {
			return errors.WithStack(err)
		}
		log.FromContext(ctx).
			WithField("link.Name", l.Attrs().Name).
			WithField("duration", time.Since(now)).
			WithField("netlink", "LinkSetUp").Debug("completed")

		// Store the link info in the cache
		link.Store(ctx, isSrc, l)
	}
	return nil
}

func Delete(ctx context.Context, conn *networkservice.Connection, isSrc bool) error {
	if mechanism := kernel.ToMechanism(conn.GetMechanism()); mechanism != nil {
		log.FromContext(ctx).Infof("veth delete: isSrc: %v, mech: %v", isSrc, mechanism)
		// Construct the netlink handle for the target namespace for this kernel interface
		handle, err := kernellink.GetNetlinkHandle(mechanism.GetNetNSURL())
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
			if link.Attrs().Name == mechanism.GetInterfaceName() {
				linkToDel = link
				break
			}
		}

		if linkToDel == nil {
			log.FromContext(ctx).
				WithField("link.Name", mechanism.GetInterfaceName()).
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
			WithField("netlink", "LinkDel").Debug("completed")

		link.Delete(ctx, isSrc)
	}

	return nil
}

func linuxIfaceName(ifaceName string) string {
	if len(ifaceName) <= kernel.LinuxIfMaxLength {
		return ifaceName
	}
	return ifaceName[:kernel.LinuxIfMaxLength]
}
