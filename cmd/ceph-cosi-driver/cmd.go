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

	"github.com/ceph/cosi-driver/pkg/driver"

	"k8s.io/klog/v2"

	"sigs.k8s.io/container-object-storage-interface/sidecar/pkg/provisioner"
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

	server, err := provisioner.NewDefaultCOSIProvisionerServer(*driverAddress,
		identityServer,
		bucketProvisioner)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}
