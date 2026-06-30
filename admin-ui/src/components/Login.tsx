import { useState } from "react";
import { localTokenProvider } from "../auth";

// Default login: the operator pastes a bearer token (e.g. an admin JWT).
// Integrators replacing the TokenProvider would typically replace this screen
// too (redirect to SSO, exchange a session cookie, etc.).
export function Login() {
  const [token, setToken] = useState("");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (token.trim()) localTokenProvider.setToken(token.trim());
  };

  return (
    <div className="login">
      <form onSubmit={submit} className="card">
        <h1>Service Constructor</h1>
        <p className="muted">Admin console</p>
        <label>
          Admin token
          <textarea
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Paste your bearer token (JWT)"
            rows={4}
          />
        </label>
        <button type="submit" disabled={!token.trim()}>
          Sign in
        </button>
      </form>
    </div>
  );
}
