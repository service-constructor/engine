import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Service, ServiceInput } from "../api/types";
import { ServiceForm } from "../components/ServiceForm";
import { KeyDialog } from "../components/KeyDialog";

type View =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "edit"; service: Service };

const STATUS_LABELS: Record<string, string> = {
  SERVICE_STATUS_DRAFT: "Draft",
  SERVICE_STATUS_ACTIVE: "Active",
  SERVICE_STATUS_SUSPENDED: "Suspended",
};

export function ServicesPage() {
  const [services, setServices] = useState<Service[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>({ kind: "list" });
  const [keyDialogFor, setKeyDialogFor] = useState<Service | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.listServices({ pageSize: 100 });
      setServices(res.services ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const create = async (input: ServiceInput) => {
    await api.createService(input);
    setView({ kind: "list" });
    await load();
  };

  const update = async (id: string, input: ServiceInput) => {
    await api.updateService(id, input);
    setView({ kind: "list" });
    await load();
  };

  const remove = async (svc: Service) => {
    if (!confirm(`Delete service "${svc.name}"?`)) return;
    await api.deleteService(svc.serviceId);
    await load();
  };

  if (view.kind === "create") {
    return <ServiceForm onSubmit={create} onCancel={() => setView({ kind: "list" })} />;
  }
  if (view.kind === "edit") {
    return (
      <ServiceForm
        initial={view.service}
        onSubmit={(input) => update(view.service.serviceId, input)}
        onCancel={() => setView({ kind: "list" })}
      />
    );
  }

  return (
    <div>
      <div className="toolbar">
        <h2>Services</h2>
        <div>
          <button className="ghost" onClick={() => void load()} disabled={loading}>
            Refresh
          </button>
          <button onClick={() => setView({ kind: "create" })}>+ New service</button>
        </div>
      </div>

      {error && <div className="error">{error}</div>}
      {loading ? (
        <p className="muted">Loading…</p>
      ) : services.length === 0 ? (
        <p className="muted">No services yet. Create one to get started.</p>
      ) : (
        <table className="grid">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Keys</th>
              <th>Execute URL</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {services.map((s) => (
              <tr key={s.serviceId}>
                <td>
                  <div className="strong">{s.name}</div>
                  <div className="mono muted">{s.serviceId}</div>
                </td>
                <td>
                  <span className={`badge ${s.status}`}>
                    {STATUS_LABELS[s.status] ?? s.status}
                  </span>
                </td>
                <td>{s.publicKeys?.length ?? 0}</td>
                <td className="mono muted">{s.executeUrl || "—"}</td>
                <td className="rowactions">
                  <button className="link" onClick={() => setKeyDialogFor(s)}>
                    Keys
                  </button>
                  <button className="link" onClick={() => setView({ kind: "edit", service: s })}>
                    Edit
                  </button>
                  <button className="link danger" onClick={() => void remove(s)}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {keyDialogFor && (
        <KeyDialog
          serviceId={keyDialogFor.serviceId}
          serviceName={keyDialogFor.name}
          onClose={() => setKeyDialogFor(null)}
          onKeyAdded={() => void load()}
        />
      )}
    </div>
  );
}
