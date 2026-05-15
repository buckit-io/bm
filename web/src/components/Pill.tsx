import "./Pill.css";

export type PillTone = "neutral" | "success" | "warning" | "danger" | "info" | "accent";

interface PillProps {
  tone?: PillTone;
  icon?: string;
  children: React.ReactNode;
}

export function Pill({ tone = "neutral", icon, children }: PillProps) {
  return (
    <span className={`pill pill--${tone}`} role="status">
      {icon && <span aria-hidden className="pill__icon">{icon}</span>}
      <span>{children}</span>
    </span>
  );
}
