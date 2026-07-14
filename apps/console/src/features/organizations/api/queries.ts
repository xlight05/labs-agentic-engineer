/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useQuery } from "@tanstack/react-query";
import { client } from "../../../api/client";
import { organizationKeys } from "./keys";
import { apiErrorMessage } from "../../../api/errors";

// Orgs for the header switcher. Near-static reference data → long staleTime.
// Note: the BFF lists every org it can see, not the caller's memberships —
// fine for the issue #91 display list; membership filtering is issue #92.
export function useOrganizations() {
  return useQuery({
    queryKey: organizationKeys.list(),
    queryFn: async () => {
      const { data, error } = await client.GET("/organizations");
      if (error) {
        throw new Error(
          apiErrorMessage(error, "Failed to load organizations"),
        );
      }
      return data;
    },
    staleTime: 5 * 60_000,
  });
}
