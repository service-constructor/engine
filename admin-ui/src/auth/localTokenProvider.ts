import type { TokenProvider } from "./types";

const STORAGE_KEY = "sc_admin_token";

// localTokenProvider is the default TokenProvider: it persists a bearer token in
// localStorage. It is paired with the built-in login form, where an operator
// pastes/obtains a JWT. Replace this module to integrate an existing auth system.
class LocalTokenProvider implements TokenProvider {
  private listeners = new Set<() => void>();

  getToken(): string | null {
    return localStorage.getItem(STORAGE_KEY);
  }

  isAuthenticated(): boolean {
    return !!this.getToken();
  }

  setToken(token: string): void {
    localStorage.setItem(STORAGE_KEY, token);
    this.emit();
  }

  signOut(): void {
    localStorage.removeItem(STORAGE_KEY);
    this.emit();
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(): void {
    this.listeners.forEach((l) => l());
  }
}

export const localTokenProvider = new LocalTokenProvider();
