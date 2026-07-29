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

package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/wso2/aep/aepctl/internal/adminpb"
	k8s "github.com/wso2/aep/aepctl/internal/kubernetes"
	"k8s.io/client-go/kubernetes"
)

type server struct {
	adminpb.UnimplementedAEPAdminServer

	k8s *kubernetes.Clientset

	openbaoAddr    string
	aepNamespace   string
	esoNamespace   string
	esoSA          string
	thunderNS      string
	thunderRelease string
	consoleURL     string
	// localStubs: LOCAL DEV ONLY. When true, Init keeps the OpenBao root token
	// (does not revoke it) so the in-cluster secret-manager-api stub — which
	// authenticates with the root token — can read/write secrets. NEVER set in
	// production, where SM-API is a managed service and the root token is revoked.
	localStubs bool
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	k8sClient, err := k8s.NewInClusterClient()
	if err != nil {
		log.Error("connect to cluster", "err", err)
		os.Exit(1)
	}

	srv := &server{
		k8s:            k8sClient,
		openbaoAddr:    mustEnv("OPENBAO_ADDR"),
		aepNamespace:   mustEnv("AEP_NAMESPACE"),
		esoNamespace:   getEnv("ESO_NAMESPACE", "external-secrets"),
		esoSA:          getEnv("ESO_SA", "external-secrets"),
		thunderNS:      getEnv("THUNDER_NAMESPACE", "thunder"),
		thunderRelease: getEnv("THUNDER_RELEASE", "thunder"),
		consoleURL:     os.Getenv("CONSOLE_URL"),
		localStubs:     os.Getenv("LOCAL_STUBS_ENABLED") == "true",
	}

	lis, err := net.Listen("tcp", ":9091")
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}

	grpcSrv := grpc.NewServer()
	adminpb.RegisterAEPAdminServer(grpcSrv, srv)

	// Standard gRPC health protocol — required for Kubernetes grpc probes.
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	log.Info("AEP server listening", "addr", ":9091")
	if err := grpcSrv.Serve(lis); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", key)
		os.Exit(1)
	}
	return v
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
