interface PlaceholderProps {
  title: string;
  note?: string;
}

export function Placeholder({ title, note }: PlaceholderProps) {
  return (
    <section className="vstack" style={{ gap: "var(--s-2)" }}>
      <h1 style={{ fontSize: "var(--fs-2xl)", fontWeight: 600 }}>{title}</h1>
      <p className="muted">
        {note ?? "This screen lands in a subsequent UI prototype milestone."}
      </p>
    </section>
  );
}
