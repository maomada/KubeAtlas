import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { DiffViewer } from "./DiffViewer";

describe("DiffViewer", () => {
  it("labels added, removed, and changed values without relying on color", () => {
    const html = renderToStaticMarkup(
      <DiffViewer
        diff={{
          summary: "Three fields changed",
          fields: [
            { path: "spec.added", oldValue: null, newValue: "on" },
            { path: "spec.removed", oldValue: "on", newValue: null },
            { path: "spec.changed", oldValue: "v1", newValue: "v2" },
          ],
        }}
      />,
    );

    expect(html).toContain("新增值：");
    expect(html).toContain("移除值：");
    expect(html).toContain("旧值：");
    expect(html).toContain("新值：");
    expect(html).toContain("Three fields changed");
  });

  it("renders values kept out of the feed as a badge, not as struck-through text", () => {
    const html = renderToStaticMarkup(
      <DiffViewer
        diff={{
          summary: "pod template annotation checksum/config changed",
          fields: [
            { path: "spec.template.metadata.annotations[checksum/config]", oldValue: "(value hidden)", newValue: "(value hidden)" },
            { path: "spec.template.metadata.annotations[example.com/new]", oldValue: null, newValue: "(set)" },
            { path: "spec.template.metadata.annotations[example.com/old]", oldValue: "(set)", newValue: null },
            { path: "spec.template.spec.affinity", oldValue: "changed", newValue: "changed" },
          ],
        }}
      />,
    );

    expect(html).not.toContain("line-through");
    expect(html).not.toContain("(value hidden)");
    expect(html).not.toContain("(set)");
    expect(html.match(/值已隐藏/g)).toHaveLength(4);
    expect(html).toContain("已设置");
    expect(html).toContain("已移除");
  });

  it("names key changes instead of striking through an unchanged key list", () => {
    const html = renderToStaticMarkup(
      <DiffViewer
        diff={{
          summary: "data modified keys: [password]",
          fields: [{ path: "data (modified keys)", oldValue: ["password"], newValue: ["password"] }],
        }}
      />,
    );
    expect(html).toContain("键已修改： <span class=\"font-mono\">password</span>");
    expect(html).not.toContain("line-through");
    expect(html).not.toContain("旧值：");
  });

  it("merges added, removed and modified key lists of one field into one row", () => {
    const html = renderToStaticMarkup(
      <DiffViewer
        diff={{
          summary: "",
          fields: [
            { path: "data (added keys)", oldValue: null, newValue: ["token"] },
            { path: "data (removed keys)", oldValue: ["old"], newValue: null },
            { path: "data (modified keys)", oldValue: ["a", "b"], newValue: ["a", "b"] },
            { path: "stringData (added keys)", oldValue: null, newValue: ["extra"] },
            { path: "type", oldValue: "Opaque", newValue: "kubernetes.io/tls" },
          ],
        }}
      />,
    );
    expect(html.match(/>data</g)).toHaveLength(1);
    expect(html).toMatch(/键已修改： <span[^>]*>a, b<\/span>.*新增： <span[^>]*>token<\/span>.*移除： <span[^>]*>old<\/span>/);
    expect(html).toContain(">stringData<");
    expect(html).toContain("新增： <span class=\"font-mono\">extra</span>");
    // Ordinary fields keep the old→new rendering.
    expect(html).toContain("旧值：");
    expect(html).toContain("kubernetes.io/tls");
  });

  it("keeps the old→new rendering when a key-list path does not carry a list", () => {
    const html = renderToStaticMarkup(
      <DiffViewer diff={{ summary: "", fields: [{ path: "data (modified keys)", oldValue: "x", newValue: "y" }] }} />,
    );
    expect(html).toContain("旧值：");
    expect(html).not.toContain("键已修改");
  });

});
