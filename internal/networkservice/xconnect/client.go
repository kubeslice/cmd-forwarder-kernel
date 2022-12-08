package xconnect

import (
	"context"

	//      "github.com/pkg/errors"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/log"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/mechanismmetadata"

	"google.golang.org/grpc"
)

type xconnectClient struct{}

// NewClient returns a client chain element implementing kernel mechanism with veth pair or smartvf
func NewClient() networkservice.NetworkServiceClient {
	return &xconnectClient{}
}

func (x *xconnectClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	logger := log.FromContext(ctx).WithField("xconnectClient", "Request")
	logger.Infof("BBH: Request: %v", request)
	conn, err := next.Client(ctx).Request(ctx, request, opts...)
	if err != nil {
		return nil, err
	}

	logger.Infof("BBH2: conn: %v", conn)

	mechanismmetadata.Store(ctx, true, conn.GetMechanism())

	/*

	   if err := create(ctx, conn, metadata.IsClient(v)); err != nil {
	           if _, closeErr := v.Close(ctx, conn, opts...); closeErr != nil {
	                   err = errors.Wrapf(err, "connection closed with error: %s", closeErr.Error())
	           }

	           return nil, err
	   }
	*/

	return conn, nil
}

func (x *xconnectClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	//logger := log.FromContext(ctx).WithField("vethClient", "Close")
	_, err := next.Client(ctx).Close(ctx, conn, opts...)

	mechanismmetadata.Delete(ctx, true)

	return &empty.Empty{}, err
}
