package xconnect

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/mechanismmetadata"

	"google.golang.org/grpc"
)

type xconnectClient struct{}

// NewClient returns a client chain element implementing kernel mechanism with veth pair or smartvf
func NewClient() networkservice.NetworkServiceClient {
	return &xconnectClient{}
}

func (x *xconnectClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	conn, err := next.Client(ctx).Request(ctx, request, opts...)
	if err != nil {
		return nil, err
	}

	mechanismmetadata.Store(ctx, true, conn.GetMechanism())

	return conn, nil
}

func (x *xconnectClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	_, err := next.Client(ctx).Close(ctx, conn, opts...)

	mechanismmetadata.Delete(ctx, true)

	return &empty.Empty{}, err
}
