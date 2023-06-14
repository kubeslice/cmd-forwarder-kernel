package forwarder

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	kernelmech "github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	vxlanmech "github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/vxlan"
	"github.com/networkservicemesh/sdk/pkg/networkservice/chains/client"
	"github.com/networkservicemesh/sdk/pkg/networkservice/chains/endpoint"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/connect"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/discover"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/filtermechanisms"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/mechanisms"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/mechanismtranslation"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/roundrobin"
	"github.com/networkservicemesh/sdk/pkg/networkservice/utils/metadata"
	registryclient "github.com/networkservicemesh/sdk/pkg/registry/chains/client"
	registryrecvfd "github.com/networkservicemesh/sdk/pkg/registry/common/recvfd"
	"github.com/networkservicemesh/sdk/pkg/tools/token"

	"github.com/networkservicemesh/sdk-kernel/pkg/kernel/networkservice/connectioncontextkernel"


	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/recvfd"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/sendfd"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/veth"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/mechanisms/vxlan"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/xconnect"

	"google.golang.org/grpc"
)

// Kernel endpoint server to cross-connect network service client pods to the endpoint pod
type kernelXconnectNSServer struct {
	endpoint.Endpoint
}

func newEndpoint(ctx context.Context, name string,
	authzServer networkservice.NetworkServiceServer, authzMonitorServer networkservice.MonitorConnectionServer,
	tokenGenerator token.GeneratorFunc, clientURL *url.URL, tunnelIpStr string, dialTimeout time.Duration,
	clientDialOptions ...grpc.DialOption) (endpoint.Endpoint, error) {
	nseClient := registryclient.NewNetworkServiceEndpointRegistryClient(ctx,
		registryclient.WithClientURL(clientURL),
		registryclient.WithNSEAdditionalFunctionality(registryrecvfd.NewNetworkServiceEndpointRegistryClient()),
		registryclient.WithDialOptions(clientDialOptions...),
	)
	nsClient := registryclient.NewNetworkServiceRegistryClient(ctx,
		registryclient.WithClientURL(clientURL),
		registryclient.WithDialOptions(clientDialOptions...))

	tunnelIP, err := parseTunnelIPCIDR(tunnelIpStr)
	if err != nil {
		return nil, err
	}

	rv := &kernelXconnectNSServer{}

	additionalFunctionality := []networkservice.NetworkServiceServer{
		metadata.NewServer(),
		recvfd.NewServer(),
		sendfd.NewServer(),
		discover.NewServer(nsClient, nseClient),
		roundrobin.NewServer(),
		connectioncontextkernel.NewServer(),
		xconnect.NewServer(),
		mechanisms.NewServer(map[string]networkservice.NetworkServiceServer{
			kernelmech.MECHANISM: veth.NewServer(),
			vxlanmech.MECHANISM:  vxlan.NewServer(tunnelIP),
		}),
		connect.NewServer(
			client.NewClient(ctx,
				client.WithoutRefresh(),
				client.WithName(name),
				client.WithDialOptions(clientDialOptions...),
				client.WithDialTimeout(dialTimeout),
				client.WithAdditionalFunctionality(
					mechanismtranslation.NewClient(),
					xconnect.NewClient(),
					veth.NewClient(),
					vxlan.NewClient(tunnelIP),
					filtermechanisms.NewClient(),
					recvfd.NewClient(),
					sendfd.NewClient(),
				),
			),
		),
	}

	rv.Endpoint = endpoint.NewServer(ctx, tokenGenerator,
		endpoint.WithName(name),
		endpoint.WithAuthorizeServer(authzServer),
		endpoint.WithAuthorizeMonitorConnectionServer(authzMonitorServer),
		endpoint.WithAdditionalFunctionality(additionalFunctionality...))

	return rv, nil
}

func parseTunnelIPCIDR(tunnelIPStr string) (net.IP, error) {
	var egressTunnelIP net.IP
	var err error
	if strings.Contains(tunnelIPStr, "/") {
		egressTunnelIP, _, err = net.ParseCIDR(tunnelIPStr)
	} else {
		egressTunnelIP = net.ParseIP(tunnelIPStr)
		if egressTunnelIP == nil {
			err = errors.New("tunnel IP must be set to a valid IP")
		}
	}
	return egressTunnelIP, err
}

func NewServer(ctx context.Context, name string, authzServer networkservice.NetworkServiceServer,
	authzMonitorServer networkservice.MonitorConnectionServer, tokenGenerator token.GeneratorFunc,
	clientURL *url.URL, tunnelIpStr string, dialTimeout time.Duration, clientDialOptions ...grpc.DialOption) (endpoint.Endpoint, error) {
	return newEndpoint(ctx, name, authzServer, authzMonitorServer, tokenGenerator, clientURL, tunnelIpStr, dialTimeout, clientDialOptions...)
}
