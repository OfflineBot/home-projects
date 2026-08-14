import { useEffect, useState } from "react";
import { NavLink, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { Icon } from "./components/Icon";
import { Spinner, StepUpProvider } from "./components/ui";
import { logout } from "./lib/api";
import { loadMeta, setUser, startSession, useSession } from "./lib/store";
import Accounts from "./pages/Accounts";
import Filters from "./pages/Filters";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { api } from "./lib/api";
import Dashboard from "./pages/Dashboard";
import GroupPage from "./pages/GroupPage";
import Groups from "./pages/Groups";
import Login from "./pages/Login";
import ProjectPage from "./pages/ProjectPage";
import Schedulers from "./pages/Schedulers";
import Security from "./pages/Security";
import Settings from "./pages/Settings";
import Structure from "./pages/Structure";
import CalendarOverlay from "./pages/CalendarOverlay";

export default function App() {
  const session = useSession();
  const [ready, setReady] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const location = useLocation();
  const stale = useNewerVersion();

  useEffect(() => {
    void (async () => {
      await Promise.all([startSession(), loadMeta().catch(() => undefined)]);
      setReady(true);
    })();
  }, []);

  useEffect(() => setMenuOpen(false), [location.pathname]);

  if (!ready) {
    return (
      <div style={{ display: "grid", placeItems: "center", height: "100vh" }}>
        <Spinner />
      </div>
    );
  }

  return (
    <StepUpProvider>
      <div className="layout">
        <aside className={menuOpen ? "sidebar open" : "sidebar"}>
          <div className="brand">
            <span className="dot" />
            home-projects
          </div>

          <NavLink to="/" end className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
            <Icon name="grid" size={17} /> Dashboard
          </NavLink>
          <NavLink to="/groups" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
            <Icon name="folder" size={17} /> Groups
          </NavLink>
          <NavLink to="/calendar" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
            <Icon name="calendar" size={17} /> Calendar
          </NavLink>
          <NavLink to="/structure" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
            <Icon name="map" size={17} /> Structure
          </NavLink>

          {session.user ? (
            <>
              <div className="nav-section">Outside</div>
              <NavLink to="/accounts" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
                <Icon name="key" size={17} /> Accounts
              </NavLink>
              <NavLink to="/schedulers" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
                <Icon name="clock" size={17} /> Schedulers
              </NavLink>
              <NavLink to="/filters" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
                <Icon name="search" size={17} /> Filters
              </NavLink>
              <div className="nav-section">You</div>
              <NavLink to="/security" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
                <Icon name="lock" size={17} /> Security
              </NavLink>
            </>
          ) : null}

          <NavLink to="/settings" className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")}>
            <Icon name="settings" size={17} /> Appearance
          </NavLink>

          <div style={{ marginTop: "auto", paddingTop: 14 }}>
            {session.user ? (
              <button
                className="nav-item"
                style={{ width: "100%", background: "none", cursor: "pointer" }}
                onClick={async () => {
                  await logout();
                  setUser(null);
                }}
              >
                <Icon name="logout" size={17} /> Sign out · {session.user.displayName || session.user.username}
              </button>
            ) : (
              <NavLink to="/login" className="nav-item">
                <Icon name="lock" size={17} /> Sign in
              </NavLink>
            )}
          </div>
        </aside>

        <div>
          <div className="mobile-bar">
            <button className="btn ghost icon" onClick={() => setMenuOpen((v) => !v)} aria-label="Menu">
              <Icon name="menu" />
            </button>
            <strong>home-projects</strong>
          </div>
          <main className="main">
            {stale ? (
              <div className="warning" role="status">
                A newer version of this page is on the server.{" "}
                <button className="btn small" onClick={() => window.location.reload()}>
                  Reload
                </button>
              </div>
            ) : null}
            <Boundary>
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/groups" element={<Groups />} />
              <Route path="/groups/:group" element={<GroupPage />} />
              <Route path="/groups/:group/:project/*" element={<ProjectPage />} />
              <Route path="/p/:project/*" element={<ProjectPage />} />
              <Route path="/calendar" element={<CalendarOverlay />} />
              <Route path="/structure" element={<Structure />} />
              <Route path="/accounts" element={<Accounts />} />
              <Route path="/filters" element={<Filters />} />
              <Route path="/schedulers" element={<Schedulers />} />
              <Route path="/security" element={<Security />} />
              <Route path="/settings" element={<Settings />} />
              <Route path="/login" element={<Login />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
            </Boundary>
          </main>
        </div>
      </div>
    </StepUpProvider>
  );
}

/**
 * Whether the page in front of you is older than the server.
 *
 * The entry point is never cached and names the bundle it wants; the bundle
 * loaded here knows its own name. When they stop agreeing, the server has been
 * updated under an open page — which until now showed up as a button that did
 * nothing, or a part that would not load.
 */
function useNewerVersion() {
  const [stale, setStale] = useState(false);
  useEffect(() => {
    const mine = [...document.querySelectorAll("script[src]")]
      .map((el) => (el as HTMLScriptElement).src)
      .find((src) => src.includes("/assets/"));
    if (!mine) return;
    const check = async () => {
      try {
        const html = await (await fetch("/index.html", { cache: "no-store" })).text();
        const theirs = /\/assets\/[^"']+\.js/.exec(html)?.[0];
        if (theirs && !mine.endsWith(theirs)) setStale(true);
      } catch {
        /* offline is not stale */
      }
    };
    void check();
    const timer = setInterval(check, 60_000);
    return () => clearInterval(timer);
  }, []);
  return stale;
}

/**
 * A blank page is the worst way to fail: it says nothing, and it hides which
 * part broke. Anything that throws while rendering ends up here instead, with
 * its message, and the rest of the app keeps working.
 */
class Boundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("render failed", error, info.componentStack);
    // And to the server log, so the person who can fix it sees the sentence
    // rather than a description of it.
    void api("/api/client-errors", {
      body: {
        message: error.message,
        where: location.pathname + location.search,
        stack: info.componentStack ?? error.stack ?? "",
      },
    }).catch(() => undefined);
  }

  render() {
    if (!this.state.error) return this.props.children;
    // The commonest cause by far: the page was open while the server was
    // updated, so a part it goes to fetch is no longer there under that name.
    const stale = /dynamically imported module|Importing a module script failed|Loading chunk/i.test(
      this.state.error.message,
    );
    return (
      <div className="error" style={{ margin: 20 }}>
        <strong>{stale ? "This page is older than the server." : "This part could not be drawn."}</strong>
        {stale ? null : (
          <div className="mono" style={{ marginTop: 6 }}>
            {this.state.error.message}
          </div>
        )}
        <button className="btn small" style={{ marginTop: 10 }} onClick={() => window.location.reload()}>
          Reload
        </button>
      </div>
    );
  }
}
