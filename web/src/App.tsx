import { Link, Navigate, NavLink, Route, Routes } from "react-router-dom";

import { Hosts } from "./pages/Hosts";
import { Login } from "./pages/Login";
import { useAuth } from "./lib/auth";

export function App() {
  const token = useAuth((s) => s.token);

  if (!token) {
    return (
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <Header />
      <main className="flex-1 overflow-auto">
        <Routes>
          <Route path="/" element={<Hosts />} />
          <Route path="/login" element={<Navigate to="/" replace />} />
          <Route path="*" element={<Hosts />} />
        </Routes>
      </main>
    </div>
  );
}

function Header() {
  const { user, clear } = useAuth();
  const navLink =
    "px-3 py-1.5 text-sm rounded text-zinc-300 hover:text-white hover:bg-zinc-800";
  const navLinkActive = "bg-zinc-800 text-white";

  return (
    <header className="flex items-center justify-between border-b border-zinc-800 bg-zinc-900 px-4 py-2">
      <div className="flex items-center gap-4">
        <Link to="/" className="text-sm font-semibold tracking-tight">
          mon
        </Link>
        <nav className="flex items-center gap-1">
          <NavLink
            to="/"
            end
            className={({ isActive }) =>
              `${navLink} ${isActive ? navLinkActive : ""}`
            }
          >
            Hosts
          </NavLink>
        </nav>
      </div>
      <div className="flex items-center gap-3 text-sm text-zinc-400">
        <span>{user?.email}</span>
        <button
          onClick={() => clear()}
          className="rounded border border-zinc-700 px-2 py-1 text-xs hover:bg-zinc-800"
        >
          Sign out
        </button>
      </div>
    </header>
  );
}
