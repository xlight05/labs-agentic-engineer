// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package ocauth_test

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/ocauth"
)

type stubStrategy struct{}

func (stubStrategy) Decide(context.Context) ocauth.AuthMode {
	return ocauth.AuthModeServiceM2M
}

func TestRequestAuthStrategy_CompileAssert(t *testing.T) {
	var _ ocauth.RequestAuthStrategy = stubStrategy{}
	var _ ocauth.AuthProvider = (ocauth.AuthProvider)(nil)
	if (stubStrategy{}).Decide(context.Background()) != ocauth.AuthModeServiceM2M {
		t.Fatal("expected AuthModeServiceM2M")
	}
}

func TestResolveOuHandle_Precedence(t *testing.T) {
	if got := ocauth.ResolveOuHandle(&ocauth.Claims{OuHandle: "h", OuName: "n", OuId: "i"}); got != "h" {
		t.Fatalf("got %q, want h", got)
	}
	if got := ocauth.ResolveOuHandle(&ocauth.Claims{OuName: "n", OuId: "i"}); got != "n" {
		t.Fatalf("got %q, want n", got)
	}
	if got := ocauth.ResolveOuHandle(&ocauth.Claims{OuId: "i"}); got != "i" {
		t.Fatalf("got %q, want i", got)
	}
	if got := ocauth.ResolveOuHandle(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
