/*
Copyright 2021 The Ceph-COSI Authors.

Licensed under the Apache License, Version 2.0 (the "License");
You may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/ceph/cosi-driver-ceph/pkg/driver"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"
)

const provisionerName = "ceph.objectstorage.k8s.io"

var (
	driverAddress = flag.String("driver-address", "unix:///var/lib/cosi/cosi.sock", "driver address for socket")
	driverPrefix  = flag.String("driver-prefix", "", "prefix for cosi driver, e.g. <prefix>.ceph.objectstorage.k8s.io")
)

func init() {
	klog.InitFlags(nil)
	if err := flag.Set("logtostderr", "true"); err != nil {
		klog.Exitf("failed to set logtostderr flag: %v", err)
	}
	flag.Parse()
}

func run(ctx context.Context) error {
	if *driverPrefix == "" {
		return errors.New("driver prefix is missing for ceph cosi driver deployment")
	}
	driverName := *driverPrefix + "." + provisionerName
	identityServer, bucketProvisioner, err := driver.NewDriver(ctx, driverName)
	if err != nil {
		return err
	}

	listener, err := listen(*driverAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", *driverAddress, err)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	cosispec.RegisterIdentityServer(grpcServer, identityServer)
	cosispec.RegisterProvisionerServer(grpcServer, bucketProvisioner)

	errCh := make(chan error, 1)
	go func() {
		klog.InfoS("Starting gRPC server", "address", *driverAddress)
		if err := grpcServer.Serve(listener); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		klog.InfoS("Context cancelled, stopping gRPC server")
		grpcServer.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

func listen(endpoint string) (net.Listener, error) {
	proto := "unix"
	addr := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		proto = u.Scheme
		if proto == "unix" {
			addr = u.Path
		} else {
			addr = u.Host
		}
	}

	if proto == "unix" {
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove existing socket file %s: %w", addr, err)
		}
	}

	return net.Listen(proto, addr)
}
