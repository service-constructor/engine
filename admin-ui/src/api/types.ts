// Types mirroring the gRPC/HTTP contract (proto JSON mapping). Enums arrive as
// their proto string names; int64 fields (currencyId) are JSON strings.

export type ServiceStatus =
  | "SERVICE_STATUS_UNSPECIFIED"
  | "SERVICE_STATUS_DRAFT"
  | "SERVICE_STATUS_ACTIVE"
  | "SERVICE_STATUS_SUSPENDED";

export type KeyAlgorithm =
  | "KEY_ALGORITHM_UNSPECIFIED"
  | "KEY_ALGORITHM_ED25519"
  | "KEY_ALGORITHM_EC_P256";

export interface PublicKey {
  kid: string;
  pem: string;
}

export interface ReceivingWallet {
  currencyId: string;
  walletId: string;
}

export interface Fee {
  percent?: string;
  fixed?: string;
}

export interface Limits {
  maxAmount?: string;
  perHour?: number;
}

export interface Service {
  serviceId: string;
  /** Account that owns the service (server-assigned). */
  ownerId?: string;
  name: string;
  publicKeys: PublicKey[];
  origins: string[];
  executeUrl: string;
  statusUrl: string;
  receivingWallets: ReceivingWallet[];
  fee?: Fee;
  limits?: Limits;
  status: ServiceStatus;
  createdAt?: string;
  updatedAt?: string;
}

export interface ListServicesResponse {
  services: Service[];
  nextPageToken: string;
}

export interface GenerateServiceKeyResponse {
  publicKey: PublicKey;
  privateKeyPem: string;
  service: Service;
}

// Fields editable in the create/edit form.
export type ServiceInput = Pick<
  Service,
  | "name"
  | "origins"
  | "executeUrl"
  | "statusUrl"
  | "receivingWallets"
  | "fee"
  | "limits"
  | "status"
>;
