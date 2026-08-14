import { Icon } from "../components/Icon";
import { ACCENTS, FLAVORS, colorVar, useTheme } from "../lib/theme";

/**
 * Appearance. Catppuccin is the colour basis and Mocha the default; all four
 * flavours are switchable, and dark stays the default even if the device says
 * otherwise. The choice belongs to the user, so it is kept locally and works
 * for visitors without an account too.
 */
export default function Settings() {
  const { flavor, accent, setFlavor, setAccent } = useTheme();

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Appearance</h1>
        </div>
      </div>

      <h2 style={{ fontSize: 17 }}>Flavour</h2>
      <div className="tiles" style={{ marginBottom: 28 }}>
        {FLAVORS.map((f) => (
          <button
            key={f.key}
            className="tile"
            style={{
              textAlign: "left",
              cursor: "pointer",
              borderColor: flavor === f.key ? "var(--accent)" : undefined,
            }}
            onClick={() => setFlavor(f.key)}
          >
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name={f.dark ? "flame" : "lightbulb"} />
              </span>
              <div>
                <h3>{f.title}</h3>
                <div className="sub">{f.note}</div>
              </div>
              {flavor === f.key ? <Icon name="check" /> : null}
            </div>
            {/* A preview of the flavour, drawn with that flavour's own variables. */}
            <div data-flavor={f.key} style={{ display: "flex", gap: 6 }}>
              {["base", "surface0", "text", "mauve", "blue", "green", "peach", "red"].map((name) => (
                <span
                  key={name}
                  title={name}
                  style={{
                    width: 22,
                    height: 22,
                    borderRadius: 6,
                    background: `var(--ctp-${name})`,
                    border: "1px solid var(--ctp-surface1)",
                  }}
                />
              ))}
            </div>
          </button>
        ))}
      </div>

      <h2 style={{ fontSize: 17 }}>Accent</h2>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        Colours buttons, focus and active states. Group and project colours come from the same palette,
        so the tiles match in every flavour.
      </p>
      <div className="swatches" style={{ marginBottom: 28 }}>
        {ACCENTS.map((name) => (
          <button
            key={name}
            className={accent === name ? "swatch selected" : "swatch"}
            style={{ background: colorVar(name) }}
            title={name}
            aria-label={name}
            onClick={() => setAccent(name)}
          />
        ))}
      </div>

      <div className="notice">
        <strong>Contrast.</strong> All four flavours are checked against WCAG AA, Latte included, and the
        focus ring stays visible in every one of them.
      </div>
    </>
  );
}
