package vxlan

import (
	"context"
	"net"

	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/payload"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/mechanisms/vxlan/vni"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

type vxlanClient struct {
}

// NewClient - returns a new client for the vxlan remote mechanism
func NewClient(tunnelIP net.IP) networkservice.NetworkServiceClient {
	return chain.NewNetworkServiceClient(
		&vxlanClient{},
		vni.NewClient(tunnelIP, vni.WithTunnelPort(vxlanDefaultPort)),
	)
}

func (v *vxlanClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	if request.GetConnection().GetPayload() != payload.Ethernet {
		return next.Client(ctx).Request(ctx, request, opts...)
	}

	mechanism := &networkservice.Mechanism{
		Cls:        cls.REMOTE,
		Type:       MECHANISM,
		Parameters: make(map[string]string),
	}
	request.MechanismPreferences = append(request.MechanismPreferences, mechanism)

	conn, err := next.Client(ctx).Request(ctx, request, opts...)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (v *vxlanClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	return next.Client(ctx).Close(ctx, conn, opts...)
}
