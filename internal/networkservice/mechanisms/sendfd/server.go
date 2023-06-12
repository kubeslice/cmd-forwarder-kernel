package sendfd

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"

	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

type sendFDServer struct{}

// NewServer - returns server which sends any "file://" Mechanism.Parameters[common.InodeURLs]s across the connection as fds (if possible) to the client
func NewServer() networkservice.NetworkServiceServer {
	return &sendFDServer{}
}

func (s sendFDServer) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	// Call the next server chain element in the chain
	conn, err := next.Server(ctx).Request(ctx, request)
	if err != nil {
		return nil, err
	}

	// Send the FD and swap the FileURL for an InodeURL
	inodeURLToFileURLMap := make(map[string]string)
	if err := sendFDAndSwapFileToInode(conn.GetMechanism().GetParameters(), inodeURLToFileURLMap); err != nil {
		return nil, err
	}
	return conn, nil
}

func (s sendFDServer) Close(ctx context.Context, conn *networkservice.Connection) (*empty.Empty, error) {
	// Call the next server chain element in the chain
	_, err := next.Server(ctx).Close(ctx, conn)
	if err != nil {
		return nil, err
	}

	// Send the FD and swap the FileURL for an InodeURL
	inodeURLToFileURLMap := make(map[string]string)
	if err := sendFDAndSwapFileToInode(conn.GetMechanism().GetParameters(), inodeURLToFileURLMap); err != nil {
		return nil, err
	}

	return &empty.Empty{}, nil
}

