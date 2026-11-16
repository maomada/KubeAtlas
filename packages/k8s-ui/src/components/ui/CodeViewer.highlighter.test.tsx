// @vitest-environment jsdom

import { expect, it, vi } from "vitest";
import { createHighlighter } from "shiki";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";

it("highlights all supported languages with the JavaScript engine and no WebAssembly", async () => {
  const instantiate = vi.spyOn(WebAssembly, "instantiate");
  const highlighter = await createHighlighter({
    langs: [],
    themes: [],
    engine: createJavaScriptRegexEngine(),
  });

  try {
    const cases = [
      { language: "yaml" as const, code: "kind: ServiceAccount\nnamespace: default", lines: 2 },
      { language: "json" as const, code: '{"kind": "ServiceAccount"}', lines: 1 },
      { language: "bash" as const, code: "kubectl get pods", lines: 1 },
      { language: "text" as const, code: "plain text", lines: 1 },
    ];

    for (const testCase of cases) {
      await highlighter.loadLanguage(testCase.language);
      await highlighter.loadTheme("github-light");
      const html = highlighter.codeToHtml(testCase.code, {
        lang: testCase.language,
        theme: "github-light",
      });
      const rendered = document.createElement("div");
      rendered.innerHTML = html;
      const shiki = rendered.querySelector("pre.shiki") as HTMLElement;
      expect(shiki).not.toBeNull();
      expect(shiki.classList.contains("github-light")).toBe(true);
      expect(shiki.querySelectorAll(".line")).toHaveLength(testCase.lines);
      if (testCase.language === "yaml") {
        expect(shiki.getAttribute("style")).toMatch(/background-color:\s*#fff/i);
        const yamlKey = [...shiki.querySelectorAll("span[style]")].find((span) => span.textContent === "kind");
        expect(yamlKey?.getAttribute("style")).toMatch(/color:\s*#22863a/i);
      }
      if (testCase.language !== "text") {
        expect(shiki.querySelectorAll('span[style*="color:"]').length).toBeGreaterThan(0);
      }
    }

    await highlighter.loadLanguage("yaml");
    await highlighter.loadTheme("github-dark");
    const darkHtml = highlighter.codeToHtml("kind: ServiceAccount", {
      lang: "yaml",
      theme: "github-dark",
    });
    const darkRendered = document.createElement("div");
    darkRendered.innerHTML = darkHtml;
    const darkShiki = darkRendered.querySelector("pre.shiki") as HTMLElement;
    expect(darkShiki.classList.contains("github-dark")).toBe(true);
    expect(darkShiki.getAttribute("style")).toMatch(/background-color:\s*#24292e/i);
    expect(darkShiki.getAttribute("style")).toMatch(/color:\s*#e1e4e8/i);
    expect(darkShiki.querySelectorAll('span[style*="color:"]').length).toBeGreaterThan(0);

    expect(instantiate).not.toHaveBeenCalled();
  } finally {
    highlighter.dispose();
    instantiate.mockRestore();
  }
});
