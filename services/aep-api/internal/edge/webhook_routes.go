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

package edge

import (
	"net/http"

	"github.com/wso2/aep/aep-api/internal/sourcecontrol/webhook"
)

// registerWebhookRoutes mounts the inbound GitHub webhook receiver. The route
// lives outside the JWT middleware — webhooks authenticate via HMAC, not JWT.
// The pattern is more specific than the JWT-gated "/api/" subtree, so
// net/http's ServeMux routes it here rather than into the auth middleware.
//
// One path serves both topologies. In cloud, the gateway's webhook endpoint is
// scoped to base path /api/v1/webhooks and forwards to the BFF verbatim, so
// GitHub deliveries arrive here (jwtAuth is disabled on that gateway endpoint;
// the per-repo webhook URL is GITHUB_WEBHOOK_DELIVERY_URL). In local/dev the
// smee-client relays to this same path, so local mirrors cloud exactly.
func registerWebhookRoutes(mux *http.ServeMux, c webhook.WebhookController) {
	mux.HandleFunc("POST /api/v1/webhooks/github", c.Receive)
}
