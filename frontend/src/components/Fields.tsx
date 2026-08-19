import { useEffect, useState } from "react";
import { Icon } from "./Icon";
import { Field } from "./ui";
import { api } from "../lib/api";
import { CodeArea } from "./CodeArea";

/**
 * One field, described by the server and drawn here — and only here.
 *
 * The server already says what every setting is: its name, what to call it,
 * what kind of thing it holds and, when the answer is a choice, the choices.
 * That description was being drawn in three different places, each with its own
 * ideas — the accounts form typed everything into text boxes, the rule editor
 * kept its own table of which parameters are really a choice, and a card's
 * settings had a third way. Three renderers for one description is how a colour
 * ends up as a line of text you have to spell "#ff8800" into.
 *
 * So there is one. Everything that shows settings uses it, and a new kind of
 * field — a colour, a list of addresses, a choice fetched from the server —
 * is added once and appears everywhere.
 */

export interface FieldSpec {
  name: string;
  label: string;
  /** text | password | number | url | textarea | code | bool | color | list | select */
  type?: string;
  placeholder?: string;
  required?: boolean;
  default?: unknown;
  hint?: string;
  options?: { value: string; label: string }[];
  /**
   * Where the choices come from when they are not known in advance:
   * "accounts:wled" is every light account, "wled:effects" the effects a WLED
   * can play. The field is still a choice — it is just one the server fills in.
   */
  from?: string;
}

type Values = Record<string, any>;

export function Fields({
  specs,
  values,
  onChange,
}: {
  specs: FieldSpec[];
  values: Values;
  onChange: (next: Values) => void;
}) {
  return (
    <>
      {specs.map((spec) => (
        <OneField
          key={spec.name}
          spec={spec}
          value={values[spec.name]}
          onChange={(v) => onChange({ ...values, [spec.name]: v })}
        />
      ))}
    </>
  );
}

export function OneField({
  spec,
  value,
  onChange,
}: {
  spec: FieldSpec;
  value: unknown;
  onChange: (next: unknown) => void;
}) {
  const fetched = useChoices(spec.from);
  const choices = spec.options?.length ? spec.options : fetched.choices;

  return (
    <Field label={spec.label} hint={spec.hint} required={spec.required} optional={!spec.required}>
      {spec.type === "code" ? (
        <CodeArea value={String(value ?? "")} onChange={(text) => onChange(text)} />
      ) : spec.type === "textarea" ? (
        <textarea rows={4} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
      ) : spec.type === "bool" ? (
        <label className="check">
          <input type="checkbox" checked={Boolean(value)} onChange={(e) => onChange(e.target.checked)} />
          <span>{spec.placeholder || "yes"}</span>
        </label>
      ) : spec.type === "color" ? (
        <Colour value={String(value ?? "")} onChange={onChange} />
      ) : spec.type === "list" ? (
        <Lines value={value} placeholder={spec.placeholder} onChange={(next) => onChange(next)} />
      ) : choices ? (
        <Choice
          choices={choices}
          value={String(value ?? spec.default ?? "")}
          loading={fetched.loading}
          allowOwn={fetched.allowOwn}
          placeholder={spec.placeholder}
          onChange={onChange}
        />
      ) : (
        <input
          type={spec.type === "password" ? "password" : spec.type === "number" ? "number" : "text"}
          placeholder={spec.placeholder}
          value={String(value ?? "")}
          onChange={(e) => onChange(spec.type === "number" ? e.target.valueAsNumber || e.target.value : e.target.value)}
        />
      )}
    </Field>
  );
}

/** A choice, with room for one the server has not heard of. */
function Choice({
  choices,
  value,
  loading,
  allowOwn,
  placeholder,
  onChange,
}: {
  choices: { value: string; label: string }[];
  value: string;
  loading?: boolean;
  allowOwn?: boolean;
  placeholder?: string;
  onChange: (next: unknown) => void;
}) {
  const known = choices.some((c) => c.value === value);
  const [own, setOwn] = useState(Boolean(value) && !known);

  if (own) {
    return (
      <div className="choice-own">
        <input placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} />
        <button className="btn small ghost" onClick={() => setOwn(false)}>
          from the list
        </button>
      </div>
    );
  }
  return (
    <div className="choice">
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{loading ? "…" : placeholder || "—"}</option>
        {choices.map((c) => (
          <option key={c.value} value={c.value}>
            {c.label}
          </option>
        ))}
      </select>
      {allowOwn ? (
        <button className="btn small ghost" title="Something not in the list" onClick={() => setOwn(true)}>
          <Icon name="plus" size={13} />
        </button>
      ) : null}
    </div>
  );
}

/** A colour, as a colour — with the hex beside it for anyone who wants it. */
function Colour({ value, onChange }: { value: string; onChange: (next: unknown) => void }) {
  const hex = /^#[0-9a-f]{6}$/i.test(value) ? value : "#b4befe";
  return (
    <div className="colour-field">
      <input type="color" value={hex} onChange={(e) => onChange(e.target.value)} aria-label="Colour" />
      <input
        className="mono"
        placeholder="#b4befe"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {value ? (
        <button className="btn small ghost" onClick={() => onChange("")}>
          none
        </button>
      ) : null}
    </div>
  );
}

/** A list of lines, with a button. A comma-separated box is a list pretending. */
function Lines({
  value,
  placeholder,
  onChange,
}: {
  value: unknown;
  placeholder?: string;
  onChange: (next: string[]) => void;
}) {
  const lines: string[] = Array.isArray(value)
    ? (value as string[])
    : String(value ?? "")
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
  const shown = lines.length ? lines : [""];

  return (
    <div className="lines">
      {shown.map((line, i) => (
        <div key={i} className="lines-row">
          <input
            className="mono"
            placeholder={placeholder}
            value={line}
            onChange={(e) => onChange(shown.map((l, j) => (j === i ? e.target.value : l)))}
          />
          <button
            className="btn ghost icon"
            aria-label="Remove this line"
            disabled={shown.length === 1}
            onClick={() => onChange(shown.filter((_, j) => j !== i))}
          >
            <Icon name="x" size={14} />
          </button>
        </div>
      ))}
      <button className="btn small ghost" onClick={() => onChange([...shown, ""])}>
        <Icon name="plus" size={13} /> One more
      </button>
    </div>
  );
}

/**
 * Choices the server fills in.
 *
 * Each source is named once here, so a field that says where its answers come
 * from works in every form without any of them knowing what a light account is.
 */
function useChoices(from?: string) {
  const [choices, setChoices] = useState<{ value: string; label: string }[] | null>(null);
  const [loading, setLoading] = useState(Boolean(from));

  useEffect(() => {
    if (!from) return;
    let alive = true;
    const load = async () => {
      try {
        if (from.startsWith("accounts:")) {
          const kind = from.slice("accounts:".length);
          const answer = await api<{ accounts: { id: string; title: string; kind: string }[] }>("/api/accounts");
          const mine = answer.accounts.filter((a) => a.kind === kind);
          if (alive) setChoices(mine.map((a) => ({ value: a.id, label: a.title })));
          return;
        }
        if (from === "wled:effects") {
          const answer = await api<{ effects: string[] }>("/api/capabilities/automation/wled/effects");
          if (alive) setChoices(answer.effects.map((name, i) => ({ value: String(i), label: `${i} · ${name}` })));
          return;
        }
      } catch {
        if (alive) setChoices([]);
      } finally {
        if (alive) setLoading(false);
      }
    };
    void load();
    return () => {
      alive = false;
    };
  }, [from]);

  // A source that is not known is not a choice at all — the field stays a line
  // to type into rather than an empty select nobody can get past.
  return {
    choices: from ? (choices ?? []) : null,
    loading,
    allowOwn: Boolean(from),
  };
}
