import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useLogin } from "../api/hooks";
import "./Login.css";

export function Login() {
  const [user, setUser] = useState("admin");
  const [pass, setPass] = useState("admin");
  const [error, setError] = useState<string | null>(null);
  const login = useLogin();
  const navigate = useNavigate();

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    login.mutate(
      { user, pass },
      {
        onSuccess: (r) => {
          if (r.ok) navigate("/clusters");
          else setError(r.error);
        },
      },
    );
  }

  return (
    <div className="login">
      <form className="card login__card" onSubmit={onSubmit}>
        <div className="login__brand">
          <span className="login__mark">B</span>
          <h1 className="login__title">Buckit Manager</h1>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="user">Username</label>
          <input
            id="user"
            className="input"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            autoComplete="username"
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="pass">Password</label>
          <input
            id="pass"
            type="password"
            className="input"
            value={pass}
            onChange={(e) => setPass(e.target.value)}
            autoComplete="current-password"
          />
        </div>
        {error && <div className="login__error" role="alert">{error}</div>}
        <button
          type="submit"
          className="btn btn--primary"
          disabled={login.isPending}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
        <p className="login__hint">
          First time? See the <a href="https://github.com/buckit-io/buckit" target="_blank" rel="noreferrer">setup guide ↗</a>
        </p>
      </form>
      <p className="login__demo subtle">
        Demo credentials: <span className="mono">admin / admin</span>
      </p>
    </div>
  );
}
