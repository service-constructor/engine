import { useAuth } from "./auth/useAuth";
import { Login } from "./components/Login";
import { ServicesPage } from "./pages/ServicesPage";

export function App() {
  const { isAuthenticated, signOut } = useAuth();

  if (!isAuthenticated) {
    return <Login />;
  }

  return (
    <div className="app">
      <header className="appbar">
        <span className="brand">Service Constructor · Admin</span>
        <button className="ghost" onClick={signOut}>
          Sign out
        </button>
      </header>
      <main className="content">
        <ServicesPage />
      </main>
    </div>
  );
}
