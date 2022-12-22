/*
 *  Copyright (c) 2022 Avesha, Inc. All rights reserved.
 *
 *  SPDX-License-Identifier: Apache-2.0
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 */

package main

import (
	"context"
	"crypto/tls"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/edwarnicke/grpcfd"
	"github.com/kelseyhightower/envconfig"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/networkservice/chains/forwarder"
	registryapi "github.com/networkservicemesh/api/pkg/api/registry"
	"github.com/networkservicemesh/sdk/pkg/networkservice/chains/endpoint"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/authorize"
	registryclient "github.com/networkservicemesh/sdk/pkg/registry/chains/client"
	"github.com/networkservicemesh/sdk/pkg/registry/common/sendfd"
	"github.com/networkservicemesh/sdk/pkg/tools/grpcutils"
	"github.com/networkservicemesh/sdk/pkg/tools/log"
	"github.com/networkservicemesh/sdk/pkg/tools/log/logruslogger"
	monitorauthorize "github.com/networkservicemesh/sdk/pkg/tools/monitorconnection/authorize"
	"github.com/networkservicemesh/sdk/pkg/tools/spiffejwt"
	"github.com/networkservicemesh/sdk/pkg/tools/spire"
	"github.com/networkservicemesh/sdk/pkg/tools/token"
	"github.com/networkservicemesh/sdk/pkg/tools/tracing"
	"github.com/sirupsen/logrus"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Config - configuration for cmd-forwarder-kernel
type Config struct {
	Name             string            `default:"forwarder" desc:"Name of Endpoint"`
	Labels           map[string]string `default:"p2p:true" desc:"Labels related to this forwarder instance"`
	NSName           string            `default:"forwarder" desc:"Name of Network Service to Register with Registry"`
	TunnelIP         string            `desc:"IP or CIDR to use for vxlan tunnels" split_words:"true"`
	ConnectTo        url.URL           `default:"unix:///connect.to.socket" desc:"url to connect to" split_words:"true"`
	LogLevel         string            `default:"INFO" desc:"Log level" split_words:"true"`
	MaxTokenLifetime time.Duration     `default:"24h" desc:"maximum lifetime of tokens" split_words:"true"`
	DialTimeout      time.Duration     `default:"50ms" desc:"Timeout for the dial the next endpoint" split_words:"true"`
}

func main() {
	// ********************************************************************************
	// setup context to catch signals
	// ********************************************************************************
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		// More Linux signals here
		syscall.SIGHUP,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	)
	defer cancel()

	// ********************************************************************************
	// setup logging
	// ********************************************************************************
	logrus.SetFormatter(&nested.Formatter{})
	ctx = log.WithLog(ctx, logruslogger.New(ctx, map[string]interface{}{"cmd": os.Args[0]}))

	starttime := time.Now()

	// ********************************************************************************
	log.FromContext(ctx).Infof("executing phase 1: get config from environment (time since start: %s)", time.Since(starttime))
	// ********************************************************************************
	now := time.Now()
	config := &Config{}
	if err := envconfig.Usage("nsm", config); err != nil {
		logrus.Fatal(err)
	}
	if err := envconfig.Process("nsm", config); err != nil {
		logrus.Fatalf("error processing config from env: %+v", err)
	}

	log.FromContext(ctx).Infof("Config: %#v", config)
	level, err := logrus.ParseLevel(config.LogLevel)
	if err != nil {
		logrus.Fatalf("invalid log level %s", config.LogLevel)
	}
	logrus.SetLevel(level)
	log.EnableTracing(level == logrus.TraceLevel)
	log.FromContext(ctx).WithField("duration", time.Since(now)).Infof("completed phase 1: get config from environment")

	// ********************************************************************************
	log.FromContext(ctx).Infof("executing phase 2: retrieving svid, check spire agent logs if this is the last line you see (time since start: %s)", time.Since(starttime))
	// ********************************************************************************
	now = time.Now()

	source, err := workloadapi.NewX509Source(ctx)
	if err != nil {
		logrus.Fatalf("error getting x509 source: %+v", err)
	}
	svid, err := source.GetX509SVID()
	if err != nil {
		logrus.Fatalf("error getting x509 svid: %+v", err)
	}
	logrus.Infof("SVID: %q", svid.ID)

	log.FromContext(ctx).WithField("duration", time.Since(now)).Info("completed phase 2: retrieving svid")

	// ********************************************************************************
	log.FromContext(ctx).Infof("executing phase 3: create xconnect network service endpoint (time since start: %s)", time.Since(starttime))
	// ********************************************************************************
	tlsClientConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeAny())
	tlsClientConfig.MinVersion = tls.VersionTLS12
	tlsServerConfig := tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeAny())
	tlsServerConfig.MinVersion = tls.VersionTLS12

	xConnectEndpoint, err := createKernelForwarderEndpoint(ctx, config, tlsClientConfig, source)
	if err != nil {
		logrus.Fatalf("error configuring forwarder endpoint: %+v", err)
	}
	log.FromContext(ctx).WithField("duration", time.Since(now)).Info("completed phase 3: create xconnect network service endpoint")

	// ********************************************************************************
	log.FromContext(ctx).Infof("executing phase 4: create grpc server and register xconnect (time since start: %s)", time.Since(starttime))
	// ********************************************************************************
	tmpDir, err := ioutil.TempDir("", "cmd-forwarder-kernel")
	if err != nil {
		log.FromContext(ctx).Fatalf("error creating tmpDir: %+v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	listenOn := &url.URL{Scheme: "unix", Path: path.Join(tmpDir, "listen_on.io.sock")}

	server := grpc.NewServer(append(
		tracing.WithTracing(),
		grpc.Creds(
			grpcfd.TransportCredentials(credentials.NewTLS(tlsServerConfig)),
		),
	)...)
	xConnectEndpoint.Register(server)
	srvErrCh := grpcutils.ListenAndServe(ctx, listenOn, server)
	exitOnErrCh(ctx, cancel, srvErrCh)
	log.FromContext(ctx).WithField("duration", time.Since(now)).Info("completed phase 4: create grpc server and register xconnect")

	// ********************************************************************************
	log.FromContext(ctx).Infof("executing phase 5: register %s with the registry (time since start: %s)", config.NSName, time.Since(starttime))
	// ********************************************************************************
	err = registerEndpoint(ctx, config, tlsClientConfig, listenOn)
	if err != nil {
		log.FromContext(ctx).Fatalf("failed to connect to registry: %+v", err)
	}
	log.FromContext(ctx).WithField("duration", time.Since(now)).Infof("completed phase 5: register %s with the registry", config.NSName)

	log.FromContext(ctx).Infof("Startup completed in %v", time.Since(starttime))

	<-ctx.Done()
	<-srvErrCh

}

func createKernelForwarderEndpoint(ctx context.Context, config *Config, tlsClientConfig *tls.Config, source x509svid.Source) (xConnectEndpoint endpoint.Endpoint, err error) {
	var spiffeidmap spire.SpiffeIDConnectionMap
	return forwarder.NewServer(
		ctx,
		config.Name,
		authorize.NewServer(authorize.WithSpiffeIDConnectionMap(&spiffeidmap)),
		monitorauthorize.NewMonitorConnectionServer(monitorauthorize.WithSpiffeIDConnectionMap(&spiffeidmap)),
		spiffejwt.TokenGeneratorFunc(source, config.MaxTokenLifetime),
		&config.ConnectTo,
		config.TunnelIP,
		config.DialTimeout,
		grpc.WithBlock(),
		grpc.WithTransportCredentials(
			grpcfd.TransportCredentials(credentials.NewTLS(tlsClientConfig))),
		grpc.WithDefaultCallOptions(
			grpc.PerRPCCredentials(token.NewPerRPCCredentials(spiffejwt.TokenGeneratorFunc(source, config.MaxTokenLifetime))),
		),
		grpcfd.WithChainStreamInterceptor(),
		grpcfd.WithChainUnaryInterceptor(),
	)
}

func registerEndpoint(ctx context.Context, cfg *Config, tlsClientConfig *tls.Config, listenOn *url.URL) error {
	clientOptions := append(
		tracing.WithTracingDial(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
		grpc.WithTransportCredentials(
			grpcfd.TransportCredentials(
				credentials.NewTLS(tlsClientConfig),
			),
		),
	)

	registryClient := registryclient.NewNetworkServiceEndpointRegistryClient(ctx, registryclient.WithClientURL(&cfg.ConnectTo),
		registryclient.WithDialOptions(clientOptions...),
		registryclient.WithNSEAdditionalFunctionality(
			sendfd.NewNetworkServiceEndpointRegistryClient(),
		),
	)
	_, err := registryClient.Register(ctx, &registryapi.NetworkServiceEndpoint{
		Name: cfg.Name,
		NetworkServiceLabels: map[string]*registryapi.NetworkServiceLabels{
			cfg.NSName: {
				Labels: cfg.Labels,
			},
		},
		NetworkServiceNames: []string{cfg.NSName},
		Url:                 grpcutils.URLToTarget(listenOn),
	})
	if err != nil {
		log.FromContext(ctx).Fatalf("failed to connect to registry: %+v", err)
	}

	return err
}

func exitOnErrCh(ctx context.Context, cancel context.CancelFunc, errCh <-chan error) {
	// If we already have an error, log it and exit
	select {
	case err := <-errCh:
		log.FromContext(ctx).Fatal(err)
	default:
	}
	// Otherwise wait for an error in the background to log and cancel
	go func(ctx context.Context, errCh <-chan error) {
		err := <-errCh
		log.FromContext(ctx).Error(err)
		cancel()
	}(ctx, errCh)
}
