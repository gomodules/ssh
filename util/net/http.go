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

package net

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http2"
	"k8s.io/klog/v2"
)

var defaultTransport = http.DefaultTransport.(*http.Transport)

// SetOldTransportDefaults applies the defaults from http.DefaultTransport
// for the Proxy, Dial, and TLSHandshakeTimeout fields if unset
func SetOldTransportDefaults(t *http.Transport) *http.Transport {
	if t.Proxy == nil || isDefault(t.Proxy) {
		// http.ProxyFromEnvironment doesn't respect CIDRs and that makes it impossible to exclude things like pod and service IPs from proxy settings
		// ProxierWithNoProxyCIDR allows CIDR rules in NO_PROXY
		t.Proxy = NewProxierWithNoProxyCIDR(http.ProxyFromEnvironment)
	}
	// If no custom dialer is set, use the default context dialer
	if t.DialContext == nil && t.Dial == nil {
		t.DialContext = defaultTransport.DialContext
	}
	if t.TLSHandshakeTimeout == 0 {
		t.TLSHandshakeTimeout = defaultTransport.TLSHandshakeTimeout
	}
	if t.IdleConnTimeout == 0 {
		t.IdleConnTimeout = defaultTransport.IdleConnTimeout
	}
	return t
}

// SetTransportDefaults applies the defaults from http.DefaultTransport
// for the Proxy, Dial, and TLSHandshakeTimeout fields if unset
func SetTransportDefaults(t *http.Transport) *http.Transport {
	t = SetOldTransportDefaults(t)
	// Allow clients to disable http2 if needed.
	if s := os.Getenv("DISABLE_HTTP2"); len(s) > 0 {
		klog.Infof("HTTP2 has been explicitly disabled")
	} else if allowsHTTP2(t) {
		if err := http2.ConfigureTransport(t); err != nil {
			klog.Warningf("Transport failed http2 configuration: %v", err)
		}
	}
	return t
}

func allowsHTTP2(t *http.Transport) bool {
	if t.TLSClientConfig == nil || len(t.TLSClientConfig.NextProtos) == 0 {
		// the transport expressed no NextProto preference, allow
		return true
	}
	for _, p := range t.TLSClientConfig.NextProtos {
		if p == http2.NextProtoTLS {
			// the transport explicitly allowed http/2
			return true
		}
	}
	// the transport explicitly set NextProtos and excluded http/2
	return false
}

var defaultProxyFuncPointer = fmt.Sprintf("%p", http.ProxyFromEnvironment)

// isDefault checks to see if the transportProxierFunc is pointing to the default one
func isDefault(transportProxier func(*http.Request) (*url.URL, error)) bool {
	transportProxierPointer := fmt.Sprintf("%p", transportProxier)
	return transportProxierPointer == defaultProxyFuncPointer
}

// NewProxierWithNoProxyCIDR constructs a Proxier function that respects CIDRs in NO_PROXY and delegates if
// no matching CIDRs are found
func NewProxierWithNoProxyCIDR(delegate func(req *http.Request) (*url.URL, error)) func(req *http.Request) (*url.URL, error) {
	// we wrap the default method, so we only need to perform our check if the NO_PROXY (or no_proxy) envvar has a CIDR in it
	noProxyEnv := os.Getenv("NO_PROXY")
	if noProxyEnv == "" {
		noProxyEnv = os.Getenv("no_proxy")
	}
	noProxyRules := strings.Split(noProxyEnv, ",")

	cidrs := []*net.IPNet{}
	for _, noProxyRule := range noProxyRules {
		_, cidr, _ := net.ParseCIDR(noProxyRule)
		if cidr != nil {
			cidrs = append(cidrs, cidr)
		}
	}

	if len(cidrs) == 0 {
		return delegate
	}

	return func(req *http.Request) (*url.URL, error) {
		ip := net.ParseIP(req.URL.Hostname())
		if ip == nil {
			return delegate(req)
		}

		for _, cidr := range cidrs {
			if cidr.Contains(ip) {
				return nil, nil
			}
		}

		return delegate(req)
	}
}
