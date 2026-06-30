// Pluggable authentication boundary for the admin UI.
//
// The whole app obtains its bearer token through a TokenProvider. The default
// implementation (see localTokenProvider.ts) keeps a token in localStorage and
// is paired with a tiny login form. An integrator adopting this open-source
// admin can drop in their own provider — sourcing the token from their existing
// session, an SSO cookie exchange, an OAuth flow, etc. — without touching any
// API or page code.

export interface TokenProvider {
  /** Returns the current bearer token, or null if the user is not signed in. */
  getToken(): string | null;
  /** Whether a token is currently present. */
  isAuthenticated(): boolean;
  /** Sign out / clear the token. */
  signOut(): void;
  /** Subscribe to auth-state changes; returns an unsubscribe function. */
  subscribe(listener: () => void): () => void;
}
