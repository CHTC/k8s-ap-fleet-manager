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

// Package v1alpha1 is a minimal, hand-written client type for a subset of
// Traefik's IngressRouteTCP CRD (traefik.io/v1alpha1). The CRD itself is
// defined and managed externally (see config/crds); this package only
// covers the fields this project sets, not Traefik's full schema.
package v1alpha1

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// We need to support both the "traefik.containo.us/v1alpha1" and "traefik.io/v1alpha1" group versions.
func getTraefikGroupVersion() schema.GroupVersion {
	// Check if the "traefik.containo.us/v1alpha1" group version is present in the environment.
	if group, exists := os.LookupEnv("TRAEFIK_GROUP_VERSION"); exists {
		return schema.GroupVersion{Group: group, Version: "v1alpha1"}
	}
	// Default to the "traefik.io/v1alpha1" group version.
	return schema.GroupVersion{Group: "traefik.io", Version: "v1alpha1"}
}

var (
	// SchemeBuilder registers the types in this package with a runtime.Scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: getTraefikGroupVersion()}

	// AddToScheme adds the types in this package to a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&IngressRouteTCP{}, &IngressRouteTCPList{})
}
