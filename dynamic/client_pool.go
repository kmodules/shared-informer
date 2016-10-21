/*
Copyright 2016 The Kubernetes Authors.

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

package dynamic

import (
	"sync"

	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/meta"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// ClientPool manages a pool of dynamic clients.
type ClientPool interface {
	// ClientForGroupVersionKind returns a client configured for the specified groupVersionResource.
	// Resource may be empty.
	ClientForGroupVersionResource(resource unversioned.GroupVersionResource) (*Client, error)
	// ClientForGroupVersionKind returns a client configured for the specified groupVersionKind.
	// Kind may be empty.
	ClientForGroupVersionKind(kind unversioned.GroupVersionKind) (*Client, error)
}

// APIPathResolverFunc knows how to convert a groupVersion to its API path. The Kind field is
// optional.
type APIPathResolverFunc func(kind unversioned.GroupVersionKind) string

// LegacyAPIPathResolverFunc can resolve paths properly with the legacy API.
func LegacyAPIPathResolverFunc(kind unversioned.GroupVersionKind) string {
	if len(kind.Group) == 0 {
		return "/api"
	}
	return "/apis"
}

// clientPoolImpl implements ClientPool and caches clients for the resource group versions
// is asked to retrieve. This type is thread safe.
type clientPoolImpl struct {
	lock                sync.RWMutex
	config              *rest.Config
	clients             map[unversioned.GroupVersion]*Client
	apiPathResolverFunc APIPathResolverFunc
	mapper              meta.RESTMapper
}

// NewClientPool returns a ClientPool from the specified config. It reuses clients for the the same
// group version. It is expected this type may be wrapped by specific logic that special cases certain
// resources or groups.
func NewClientPool(config *rest.Config, mapper meta.RESTMapper, apiPathResolverFunc APIPathResolverFunc) ClientPool {
	confCopy := *config
	return &clientPoolImpl{
		config:              &confCopy,
		clients:             map[unversioned.GroupVersion]*Client{},
		apiPathResolverFunc: apiPathResolverFunc,
		mapper:              mapper,
	}
}

// ClientForGroupVersionResource uses the provided RESTMapper to identify the appropriate resource. Resource may
// be empty. If no matching kind is found the underlying client for that group is still returned.
func (c *clientPoolImpl) ClientForGroupVersionResource(resource unversioned.GroupVersionResource) (*Client, error) {
	kinds, err := c.mapper.KindsFor(resource)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return c.ClientForGroupVersionKind(unversioned.GroupVersionKind{Group: resource.Group, Version: resource.Version})
		}
		return nil, err
	}
	return c.ClientForGroupVersionKind(kinds[0])
}

// ClientForGroupVersion returns a client for the specified groupVersion, creates one if none exists. Kind
// in the GroupVersionKind may be empty.
func (c *clientPoolImpl) ClientForGroupVersionKind(kind unversioned.GroupVersionKind) (*Client, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	gv := kind.GroupVersion()

	// do we have a client already configured?
	if existingClient, found := c.clients[gv]; found {
		return existingClient, nil
	}

	// avoid changing the original config
	confCopy := *c.config
	conf := &confCopy

	// we need to set the api path based on group version, if no group, default to legacy path
	conf.APIPath = c.apiPathResolverFunc(kind)

	// we need to make a client
	conf.GroupVersion = &gv

	if conf.NegotiatedSerializer == nil {
		streamingInfo, _ := api.Codecs.StreamingSerializerForMediaType("application/json;stream=watch", nil)
		conf.NegotiatedSerializer = serializer.NegotiatedSerializerWrapper(runtime.SerializerInfo{Serializer: dynamicCodec{}}, streamingInfo)
	}

	dynamicClient, err := NewClient(conf)
	if err != nil {
		return nil, err
	}
	c.clients[gv] = dynamicClient
	return dynamicClient, nil
}
