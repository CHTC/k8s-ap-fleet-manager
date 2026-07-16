/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ServiceTCP is a reference to a Kubernetes Service to route matched TCP traffic to.
type ServiceTCP struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
}

// RouteTCP is a rule matching incoming TCP traffic and the service(s) to route it to.
type RouteTCP struct {
	Match    string       `json:"match"`
	Services []ServiceTCP `json:"services,omitempty"`
}

// IngressRouteTCPSpec is the subset of Traefik's IngressRouteTCP spec used by this project.
type IngressRouteTCPSpec struct {
	EntryPoints []string   `json:"entryPoints,omitempty"`
	Routes      []RouteTCP `json:"routes"`
}

// IngressRouteTCP is a minimal, hand-written representation of Traefik's
// IngressRouteTCP CRD. It is not a complete mirror of Traefik's schema -
// only the fields this project sets are included.
type IngressRouteTCP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec IngressRouteTCPSpec `json:"spec"`
}

// IngressRouteTCPList contains a list of IngressRouteTCP.
type IngressRouteTCPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []IngressRouteTCP `json:"items"`
}

func (in *ServiceTCP) DeepCopy() *ServiceTCP {
	if in == nil {
		return nil
	}
	out := new(ServiceTCP)
	*out = *in
	return out
}

func (in *RouteTCP) DeepCopyInto(out *RouteTCP) {
	*out = *in
	if in.Services != nil {
		out.Services = make([]ServiceTCP, len(in.Services))
		copy(out.Services, in.Services)
	}
}

func (in *RouteTCP) DeepCopy() *RouteTCP {
	if in == nil {
		return nil
	}
	out := new(RouteTCP)
	in.DeepCopyInto(out)
	return out
}

func (in *IngressRouteTCPSpec) DeepCopyInto(out *IngressRouteTCPSpec) {
	*out = *in
	if in.EntryPoints != nil {
		out.EntryPoints = make([]string, len(in.EntryPoints))
		copy(out.EntryPoints, in.EntryPoints)
	}
	if in.Routes != nil {
		out.Routes = make([]RouteTCP, len(in.Routes))
		for i := range in.Routes {
			in.Routes[i].DeepCopyInto(&out.Routes[i])
		}
	}
}

func (in *IngressRouteTCPSpec) DeepCopy() *IngressRouteTCPSpec {
	if in == nil {
		return nil
	}
	out := new(IngressRouteTCPSpec)
	in.DeepCopyInto(out)
	return out
}

func (in *IngressRouteTCP) DeepCopyInto(out *IngressRouteTCP) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in *IngressRouteTCP) DeepCopy() *IngressRouteTCP {
	if in == nil {
		return nil
	}
	out := new(IngressRouteTCP)
	in.DeepCopyInto(out)
	return out
}

func (in *IngressRouteTCP) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *IngressRouteTCPList) DeepCopyInto(out *IngressRouteTCPList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]IngressRouteTCP, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *IngressRouteTCPList) DeepCopy() *IngressRouteTCPList {
	if in == nil {
		return nil
	}
	out := new(IngressRouteTCPList)
	in.DeepCopyInto(out)
	return out
}

func (in *IngressRouteTCPList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
