import { useState } from "react";
import { api } from "../api/client";
import type { GenerateServiceKeyResponse, KeyAlgorithm } from "../api/types";

interface Props {
  serviceId: string;
  serviceName: string;
  onClose: () => void;
  onKeyAdded: () => void;
}

// KeyDialog generates a new key pair on the backend and shows the private key
// PEM exactly once — the platform does not store it. The operator must download
// or copy it before closing.
export function KeyDialog({ serviceId, serviceName, onClose, onKeyAdded }: Props) {
  const [algorithm, setAlgorithm] = useState<KeyAlgorithm>("KEY_ALGORITHM_ED25519");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<GenerateServiceKeyResponse | null>(null);

  const generate = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.generateKey(serviceId, algorithm);
      setResult(res);
      onKeyAdded();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const download = () => {
    if (!result) return;
    const blob = new Blob([result.privateKeyPem], { type: "application/x-pem-file" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${result.publicKey.kid}.private.pem`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal card" onClick={(e) => e.stopPropagation()}>
        <h2>Generate key for {serviceName}</h2>

        {!result ? (
          <>
            <p className="muted">
              The platform stores only the public key. The private key is shown once
              and never persisted.
            </p>
            <label>
              Algorithm
              <select
                value={algorithm}
                onChange={(e) => setAlgorithm(e.target.value as KeyAlgorithm)}
              >
                <option value="KEY_ALGORITHM_ED25519">Ed25519 (recommended)</option>
                <option value="KEY_ALGORITHM_EC_P256">EC P-256</option>
              </select>
            </label>
            {error && <div className="error">{error}</div>}
            <div className="actions">
              <button className="ghost" onClick={onClose} disabled={busy}>
                Cancel
              </button>
              <button onClick={generate} disabled={busy}>
                {busy ? "Generating…" : "Generate"}
              </button>
            </div>
          </>
        ) : (
          <>
            <div className="warn">
              ⚠ Save this private key now. It will not be shown again.
            </div>
            <label>
              kid
              <input readOnly value={result.publicKey.kid} />
            </label>
            <label>
              Private key (PEM)
              <textarea readOnly rows={6} value={result.privateKeyPem} />
            </label>
            <div className="actions">
              <button className="ghost" onClick={() => navigator.clipboard.writeText(result.privateKeyPem)}>
                Copy
              </button>
              <button className="ghost" onClick={download}>
                Download
              </button>
              <button onClick={onClose}>Done</button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
