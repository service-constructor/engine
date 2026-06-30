import { tokenProvider } from "../auth";
import type {
  GenerateServiceKeyResponse,
  KeyAlgorithm,
  ListServicesResponse,
  Service,
  ServiceInput,
  ServiceStatus,
} from "./types";

// Base URL of the gateway. Defaults to same-origin (dev proxy / co-hosted).
const API_BASE = import.meta.env.VITE_API_BASE ?? "";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const token = tokenProvider.getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (res.status === 401) {
    // Token rejected — clear it so the app routes back to login.
    tokenProvider.signOut();
  }

  if (!res.ok) {
    let message = res.statusText;
    try {
      const data = await res.json();
      message = data.message ?? message;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, message);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  listServices(params?: { pageSize?: number; pageToken?: string; status?: ServiceStatus }) {
    const q = new URLSearchParams();
    if (params?.pageSize) q.set("pageSize", String(params.pageSize));
    if (params?.pageToken) q.set("pageToken", params.pageToken);
    if (params?.status && params.status !== "SERVICE_STATUS_UNSPECIFIED")
      q.set("status", params.status);
    const qs = q.toString();
    return request<ListServicesResponse>("GET", `/v1/admin/services${qs ? `?${qs}` : ""}`);
  },

  getService(id: string) {
    return request<Service>("GET", `/v1/admin/services/${encodeURIComponent(id)}`);
  },

  createService(input: ServiceInput) {
    return request<Service>("POST", "/v1/admin/services", input);
  },

  // PATCH uses field-mask semantics on the backend: only fields present in the
  // body change. We send the full editable set, which is fine for form saves.
  updateService(id: string, input: ServiceInput) {
    return request<Service>("PATCH", `/v1/admin/services/${encodeURIComponent(id)}`, input);
  },

  deleteService(id: string) {
    return request<void>("DELETE", `/v1/admin/services/${encodeURIComponent(id)}`);
  },

  generateKey(id: string, algorithm: KeyAlgorithm) {
    return request<GenerateServiceKeyResponse>(
      "POST",
      `/v1/admin/services/${encodeURIComponent(id)}/keys`,
      { serviceId: id, algorithm },
    );
  },

  rotateKey(id: string, algorithm: KeyAlgorithm, retireKid: string) {
    return request<GenerateServiceKeyResponse>(
      "POST",
      `/v1/admin/services/${encodeURIComponent(id)}/rotate-key`,
      { serviceId: id, algorithm, retireKid },
    );
  },
};
