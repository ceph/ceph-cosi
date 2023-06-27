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

package driver

import (
	"context"

	"k8s.io/klog/v2"
	cosispec "sigs.k8s.io/container-object-storage-interface-spec"
)

func NewDriver(ctx context.Context, provisionerName string) (cosispec.IdentityServer, cosispec.ProvisionerServer, error) {
	provisionerServer, err := NewProvisionerServer(provisionerName)
	if err != nil {
		klog.Fatal(err, "failed to create provisioner server")
		return nil, nil, err
	}
	identityServer, err := NewIdentityServer(provisionerName)
	if err != nil {
		klog.Fatal(err, "failed to create provisioner server")
		return nil, nil, err
	}
	return identityServer, provisionerServer, nil
}
