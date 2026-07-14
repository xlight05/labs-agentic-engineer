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

/**
 * Human-readable message for a failed BFF call.
 *
 * The BFF is cutting over from RFC 9457 problem-details ({title, detail})
 * to the flat {code, message} envelope; this reads both dialects during
 * the migration window. Once the backend cutover ships everywhere, drop
 * the detail/title fallback and read only `message`.
 */
export function apiErrorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === "object") {
    const e = error as Record<string, unknown>;
    for (const key of ["message", "detail", "title"] as const) {
      const v = e[key];
      if (typeof v === "string" && v.length > 0) return v;
    }
  }
  return fallback;
}
