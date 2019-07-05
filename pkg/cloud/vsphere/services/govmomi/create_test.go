/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package govmomi_test

import (
	"crypto/tls"
	"log"
	"reflect"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"
	yaml "gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"

	"sigs.k8s.io/cluster-api-provider-vsphere/pkg/apis/vsphere/v1alpha1"
	"sigs.k8s.io/cluster-api-provider-vsphere/pkg/cloud/vsphere/context"
	"sigs.k8s.io/cluster-api-provider-vsphere/pkg/cloud/vsphere/services/certificates"
	"sigs.k8s.io/cluster-api-provider-vsphere/pkg/cloud/vsphere/services/govmomi"
)

func TestCreate(t *testing.T) {
	model := simulator.VPX()
	model.Host = 0 // ClusterHost only

	defer model.Remove()
	err := model.Create()
	if err != nil {
		log.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)

	s := model.Service.NewServer()
	defer s.Close()

	pass, _ := s.URL.User.Password()
	clusterConfig := v1alpha1.VsphereClusterProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		Username: s.URL.User.Username(),
		Password: pass,
		Server:   s.URL.Host,
	}
	clusterConfig.TypeMeta.Kind = reflect.TypeOf(clusterConfig).Name()

	raw, err := yaml.Marshal(clusterConfig)
	if err != nil {
		log.Fatal(err)
	}

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: clusterv1.ClusterSpec{
			ClusterNetwork: clusterv1.ClusterNetworkingConfig{
				Services: clusterv1.NetworkRanges{
					CIDRBlocks: []string{"1.2.3.4"},
				},
				Pods: clusterv1.NetworkRanges{
					CIDRBlocks: []string{"5.6.7.8"},
				},
			},
			ProviderSpec: clusterv1.ProviderSpec{
				Value: &runtime.RawExtension{
					Raw: raw,
				},
			},
		},
		Status: clusterv1.ClusterStatus{
			ProviderStatus: &runtime.RawExtension{
				Raw: []byte(`{"clusterApiStatus": "Ready"}`),
			},
			APIEndpoints: []clusterv1.APIEndpoint{
				{
					Host: "127.0.0.1",
					Port: 0, // TODO
				},
			},
		},
	}

	clusterContext, err := context.NewClusterContext(&context.ClusterContextParams{
		Cluster: cluster,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := certificates.ReconcileCertificates(clusterContext); err != nil {
		t.Fatal(err)
	}

	vm := simulator.Map.Any("VirtualMachine").(*simulator.VirtualMachine)
	disk := object.VirtualDeviceList(vm.Config.Hardware.Device).SelectByType((*types.VirtualDisk)(nil))[0].(*types.VirtualDisk)
	disk.CapacityInKB = 20 * 1024 * 1024 // bump since default disk size is < 1GB

	machineConfig := v1alpha1.VsphereMachineProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "vsphereproviderconfig/v1alpha1",
		},
		Datacenter:   "",
		Datastore:    "",
		ResourcePool: "",
		Folder:       "",
		Network: v1alpha1.NetworkSpec{
			Devices: []v1alpha1.NetworkDeviceSpec{
				{
					NetworkName: "VM Network",
					DHCP4:       true,
					DHCP6:       true,
				},
			},
		},
		NumCPUs:   2,
		MemoryMiB: 2048,
		Template:  vm.Name,
	}
	machineConfig.TypeMeta.Kind = reflect.TypeOf(machineConfig).Name()

	raw, err = yaml.Marshal(machineConfig)
	if err != nil {
		log.Fatal(err)
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "machine1",
			Labels: map[string]string{
				"set": "controlplane",
			},
		},
		Spec: clusterv1.MachineSpec{
			ProviderSpec: clusterv1.ProviderSpec{
				Value: &runtime.RawExtension{
					Raw: raw,
				},
			},
			Versions: clusterv1.MachineVersionInfo{
				ControlPlane: "1.12.3",
				Kubelet:      "1.12.3",
			},
		},
	}

	machineContext, err := context.NewMachineContextFromClusterContext(clusterContext, machine)
	if err != nil {
		t.Fatal(err)
	}

	if err := govmomi.Create(machineContext, ""); err != nil {
		log.Fatal(err)
	}

	if model.Machine+1 != model.Count().Machine {
		t.Error("failed to clone vm")
	}
}
