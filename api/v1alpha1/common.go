/*
Copyright 2023 José Luis Salvador Rufo <salvador.joseluis@gmail.com>.

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
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

type NamespacedName struct {
	// +optional
	Namespace *string `json:"namespace,omitempty"`

	// +required
	Name string `json:"name"`
}

func (nn NamespacedName) String() string {
	return fmt.Sprintf("%s/%s", *nn.Namespace, nn.Name)
}

func (nn NamespacedName) AsType(defaultNamespace string) types.NamespacedName {
	var ns string
	if len(*nn.Namespace) > 0 {
		ns = *nn.Namespace
	} else {
		ns = defaultNamespace
	}

	return types.NamespacedName{
		Namespace: ns,
		Name:      nn.Name,
	}
}
