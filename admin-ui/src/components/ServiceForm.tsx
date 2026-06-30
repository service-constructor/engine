import { useState } from "react";
import type { Service, ServiceInput, ServiceStatus } from "../api/types";

const STATUSES: { value: ServiceStatus; label: string }[] = [
  { value: "SERVICE_STATUS_DRAFT", label: "Draft" },
  { value: "SERVICE_STATUS_ACTIVE", label: "Active" },
  { value: "SERVICE_STATUS_SUSPENDED", label: "Suspended" },
];

interface Props {
  initial?: Service;
  onSubmit: (input: ServiceInput) => Promise<void>;
  onCancel: () => void;
}

// ServiceForm handles both create and edit. Receiving wallets and origins are
// edited as simple line/comma lists and parsed on submit.
export function ServiceForm({ initial, onSubmit, onCancel }: Props) {
  const [name, setName] = useState(initial?.name ?? "");
  const [status, setStatus] = useState<ServiceStatus>(
    initial?.status ?? "SERVICE_STATUS_DRAFT",
  );
  const [executeUrl, setExecuteUrl] = useState(initial?.executeUrl ?? "");
  const [statusUrl, setStatusUrl] = useState(initial?.statusUrl ?? "");
  const [origins, setOrigins] = useState((initial?.origins ?? []).join("\n"));
  const [wallets, setWallets] = useState(
    (initial?.receivingWallets ?? []).map((w) => `${w.currencyId}:${w.walletId}`).join("\n"),
  );
  const [feePercent, setFeePercent] = useState(initial?.fee?.percent ?? "");
  const [feeFixed, setFeeFixed] = useState(initial?.fee?.fixed ?? "");
  const [maxAmount, setMaxAmount] = useState(initial?.limits?.maxAmount ?? "");
  const [perHour, setPerHour] = useState(
    initial?.limits?.perHour ? String(initial.limits.perHour) : "",
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const parsedWallets = wallets
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const [currencyId, walletId] = l.split(":");
        return { currencyId: (currencyId ?? "").trim(), walletId: (walletId ?? "").trim() };
      });

    const input: ServiceInput = {
      name: name.trim(),
      status,
      executeUrl: executeUrl.trim(),
      statusUrl: statusUrl.trim(),
      origins: origins.split("\n").map((o) => o.trim()).filter(Boolean),
      receivingWallets: parsedWallets,
      fee: { percent: feePercent.trim(), fixed: feeFixed.trim() },
      limits: {
        maxAmount: maxAmount.trim(),
        perHour: perHour.trim() ? Number(perHour) : 0,
      },
    };

    setBusy(true);
    try {
      await onSubmit(input);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="card form">
      <h2>{initial ? "Edit service" : "New service"}</h2>
      {error && <div className="error">{error}</div>}

      <label>
        Name *
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>

      <label>
        Status
        <select value={status} onChange={(e) => setStatus(e.target.value as ServiceStatus)}>
          {STATUSES.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </label>

      <label>
        Execute URL
        <input
          value={executeUrl}
          onChange={(e) => setExecuteUrl(e.target.value)}
          placeholder="https://service.example.com/execute"
        />
      </label>

      <label>
        Status URL
        <input
          value={statusUrl}
          onChange={(e) => setStatusUrl(e.target.value)}
          placeholder="https://service.example.com/status"
        />
      </label>

      <label>
        Origins (one per line)
        <textarea
          value={origins}
          onChange={(e) => setOrigins(e.target.value)}
          rows={2}
          placeholder="https://app.example.com"
        />
      </label>

      <label>
        Receiving wallets (currencyId:walletId, one per line)
        <textarea
          value={wallets}
          onChange={(e) => setWallets(e.target.value)}
          rows={2}
          placeholder="1:wlt_usdt_01"
        />
      </label>

      <div className="row">
        <label>
          Fee percent
          <input value={feePercent} onChange={(e) => setFeePercent(e.target.value)} placeholder="1.5" />
        </label>
        <label>
          Fee fixed
          <input value={feeFixed} onChange={(e) => setFeeFixed(e.target.value)} placeholder="0" />
        </label>
      </div>

      <div className="row">
        <label>
          Max amount
          <input value={maxAmount} onChange={(e) => setMaxAmount(e.target.value)} placeholder="100.00" />
        </label>
        <label>
          Per hour
          <input
            value={perHour}
            onChange={(e) => setPerHour(e.target.value)}
            placeholder="1000"
            inputMode="numeric"
          />
        </label>
      </div>

      <div className="actions">
        <button type="button" className="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" disabled={busy || !name.trim()}>
          {busy ? "Saving…" : initial ? "Save changes" : "Create"}
        </button>
      </div>
    </form>
  );
}
