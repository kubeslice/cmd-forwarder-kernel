package xconnect

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk-kernel/pkg/kernel/networkservice/connectioncontextkernel"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/log"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/veth"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/vxlan"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/mechanismmetadata"
)

type xconnectServer struct {
}

// The kernel xconnect server that cross connects client and server pods through a veth link if
// the network service client and server pods are co-located on the same node or a vxlan tunnel if
// they are located on different nodes.
// This server is inserted as a chain element in the kernel forwarder endpoint registration process.
func NewServer() networkservice.NetworkServiceServer {
	return &xconnectServer{}
}

func createConnectionWithMechanism(mech *networkservice.Mechanism, srcConn *networkservice.Connection) *networkservice.Connection {
	conn := srcConn.Clone()
	conn.Mechanism = mech.Clone()
	return conn
}

func deleteLocalConnection(ctx context.Context, srcConn, dstConn *networkservice.Connection) error {
	err := veth.Delete(ctx, srcConn, true)
	if err != nil {
		return err
	}
	err = veth.Delete(ctx, dstConn, false)
	if err != nil {
		return err
	}
	return nil
}

func deleteRemoteConnection(ctx context.Context, srcConn *networkservice.Connection, outgoing bool) error {
	err := vxlan.Delete(ctx, srcConn, outgoing)
	if err != nil {
		return err
	}
	return nil
}

func handleLocalConnection(ctx context.Context, srcConn, dstConn *networkservice.Connection, request *networkservice.NetworkServiceRequest) error {
	err := veth.Create(ctx, srcConn, true)
	if err != nil {
		return err
	}

	err = veth.Create(ctx, dstConn, false)
	if err != nil {
		return err
	}

	req2 := request.Clone()
	req2.Connection = dstConn

	connCtxClient := connectioncontextkernel.NewClient()
	_, err = connCtxClient.Request(ctx, req2)
	if err != nil {
		return err
	}

	return nil
}

func handleRemoteConnection(ctx context.Context, srcConn *networkservice.Connection, request *networkservice.NetworkServiceRequest, outgoing bool) error {
	err := vxlan.Create(ctx, srcConn, outgoing)
	if err != nil {
		return err
	}

	return nil
}

func (x *xconnectServer) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	logger := log.FromContext(ctx).WithField("xconnectServer", "Request")

	conn, err := next.Server(ctx).Request(ctx, request)
	if err != nil {
		closeConnection(ctx, request.GetConnection())
		return nil, err
	}

	// The xconnect server needs to know both the local and remote connection mechanism details. Unlike other forwarder (vpp and ovs) implementations where
	// local and remote mechanisms are honoured at different points in the forwarder chain, the kernel forwarder creates both the mechanisms
	// at one point only - here in the xconnect server. This departure from other forwarder implementations is needed because of the inherent
	// nature of link creation in the Linux kernel which does not involve creating p2p links between the pods and the forwarder, and later
	// cross connecting them at L2.
	// The mechanismmetadata cache contains remote mechanism info that is stored by the xconnect.Client.
	dstMech, _ := mechanismmetadata.Load(ctx, true)
	srcMech := conn.GetMechanism()
	logger.Debugf("srcMech: %v, dstMech: %v", srcMech, dstMech)

	// Check if the connection is LOCAL or REMOTE
	// If both the local and remote connection mechanisms are LOCAL, the connection request is considered to be LOCAL.
	// If either of them is REMOTE (the other would be LOCAL, both cannot be REMOTE), the connection request is considered REMOTE.
	if srcMech.Cls == "LOCAL" && dstMech.Cls == "LOCAL" {
		srcConn := conn
		dstConn := createConnectionWithMechanism(dstMech, conn)
		err := handleLocalConnection(ctx, srcConn, dstConn, request)
		if err != nil {
			return nil, err
		}
		// If the connection was handled successfully, we need to store the dstMech. It is needed to cleanup the connection
		// in the Close().
		mechanismmetadata.Store(ctx, false, dstMech)
	} else {
		var srcConn *networkservice.Connection
		if dstMech.Cls == "REMOTE" {
			// Create a source connection object by copying the destn mechanism info. Only the relevant mechanism info
			// is copied and the rest is populated from the mechanism present in the src mechanism object.
			// For example, the destMech would contain the ip address of the remote node to create the vxlan tunnel. It
			// would also contain the vni for the vxlan tunnel. The rest of the mechanism info like the name of the
			// interface and the target network namespace are copied from the src mechanism object.
			srcConn = createConnectionWithMechanism(dstMech, conn)
			if srcConn.GetMechanism().GetParameters() == nil {
				srcConn.GetMechanism().Parameters = make(map[string]string)
			}
			if ifaceName, ok := srcConn.GetMechanism().GetParameters()["name"]; !ok || ifaceName == "" {
				srcConn.GetMechanism().GetParameters()["name"] = srcMech.GetParameters()["name"]
			}
			if netNsUrl, ok := srcConn.GetMechanism().GetParameters()["inodeURL"]; !ok || netNsUrl == "" {
				srcConn.GetMechanism().GetParameters()["inodeURL"] = srcMech.GetParameters()["inodeURL"]
			}
		} else {
			srcConn = conn.Clone()
			if srcConn.GetMechanism().GetParameters() == nil {
				srcConn.GetMechanism().Parameters = make(map[string]string)
			}
			if ifaceName, ok := srcConn.GetMechanism().GetParameters()["name"]; !ok || ifaceName == "" {
				srcConn.GetMechanism().GetParameters()["name"] = dstMech.GetParameters()["name"]
			}
			if netNsUrl, ok := srcConn.GetMechanism().GetParameters()["inodeURL"]; !ok || netNsUrl == "" {
				srcConn.GetMechanism().GetParameters()["inodeURL"] = dstMech.GetParameters()["inodeURL"]
			}
		}

		outgoing := srcMech.GetCls() == "LOCAL"
		// For remote connections, only one interface needs to be created on the local node, hence no dstConn in the
		// handleRemoteConnection().
		err := handleRemoteConnection(ctx, srcConn, request, outgoing)
		if err != nil {
			_, errC := x.Close(ctx, srcConn)
			if errC != nil {
				logger.Errorf("Failed to close conn after request error: %v", errC)
			}
			return nil, err
		}
		// If the connection was handled successfully, we need to store the dstMech. It is needed to cleanup the connection
		// in the Close().
		mechanismmetadata.Store(ctx, false, dstMech)
		// In the kernel forwarder endpoint chain, we use the connectioncontextkernel pkg to configure the interface. But
		// that pkg ignores requests if the local/source  mechanism is REMOTE. We need to create a new request by copying
		// the dstMech info to mimic a LOCAL request.
		if srcMech.GetCls() == "REMOTE" {
			req2 := request.Clone()
			req2.Connection = createConnectionWithMechanism(dstMech, conn)
			connCtxClient := connectioncontextkernel.NewClient()
			_, err := connCtxClient.Request(ctx, req2)
			if err != nil {
				return nil, err
			}
		}
	}

	return conn, nil
}

func (x *xconnectServer) Close(ctx context.Context, conn *networkservice.Connection) (*empty.Empty, error) {

	closeConnection(ctx, conn)

	return next.Server(ctx).Close(ctx, conn)
}

func closeConnection(ctx context.Context, conn *networkservice.Connection) error {
	dstMech := mechanismmetadata.LoadAndDelete(ctx, false)
	if dstMech == nil {
		return nil
	}
	srcMech := conn.GetMechanism()
	if srcMech == nil {
		return nil
	}

	if srcMech.Cls == "LOCAL" && dstMech.Cls == "LOCAL" {
		srcConn := conn.Clone()
		dstConn := createConnectionWithMechanism(dstMech, conn)
		err := deleteLocalConnection(ctx, srcConn, dstConn)
		if err != nil {
			return err
		}
	} else {
		srcConn := conn.Clone()
		if dstMech.Cls == "REMOTE" {
			srcConn = createConnectionWithMechanism(dstMech, conn)
			if srcConn.GetMechanism().GetParameters() == nil {
				srcConn.GetMechanism().Parameters = make(map[string]string)
			}
			if ifaceName, ok := srcConn.GetMechanism().GetParameters()["name"]; !ok || ifaceName == "" {
				srcConn.GetMechanism().GetParameters()["name"] = srcMech.GetParameters()["name"]
			}
			if netNsUrl, ok := srcConn.GetMechanism().GetParameters()["inodeURL"]; !ok || netNsUrl == "" {
				srcConn.GetMechanism().GetParameters()["inodeURL"] = srcMech.GetParameters()["inodeURL"]
			}
		} else {
			if srcConn.GetMechanism().GetParameters() == nil {
				srcConn.GetMechanism().Parameters = make(map[string]string)
			}
			if ifaceName, ok := srcConn.GetMechanism().GetParameters()["name"]; !ok || ifaceName == "" {
				srcConn.GetMechanism().GetParameters()["name"] = dstMech.GetParameters()["name"]
			}
			if netNsUrl, ok := srcConn.GetMechanism().GetParameters()["inodeURL"]; !ok || netNsUrl == "" {
				srcConn.GetMechanism().GetParameters()["inodeURL"] = dstMech.GetParameters()["inodeURL"]
			}
		}
		outgoing := conn.GetMechanism().GetCls() == "LOCAL"
		err := deleteRemoteConnection(ctx, srcConn, outgoing)
		if err != nil {
			return err
		}
	}

	return nil
}
