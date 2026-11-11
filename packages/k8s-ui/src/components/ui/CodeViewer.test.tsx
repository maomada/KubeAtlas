// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { createHighlighter } from "shiki";
import { CodeViewer } from "./CodeViewer";

vi.mock("shiki", () => ({
  createHighlighter: vi.fn(),
}));
vi.mock("shiki/engine/javascript", () => ({
  createJavaScriptRegexEngine: vi.fn(() => ({})),
}));

vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);

const mockCodeToHtml = vi.fn<(code: string, options: { lang: string; theme: string }) => Promise<string>>();
const mockHighlighter = {
  codeToHtml: mockCodeToHtml,
  loadLanguage: vi.fn<(language: string) => Promise<void>>(),
  loadTheme: vi.fn<(theme: string) => Promise<void>>(),
  dispose: vi.fn(),
};
const mockHighlighterInstance = mockHighlighter as unknown as Awaited<ReturnType<typeof createHighlighter>>;

let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  document.documentElement.classList.remove("dark");
  vi.mocked(createHighlighter).mockReset().mockResolvedValue(mockHighlighterInstance);
  mockCodeToHtml.mockReset().mockResolvedValue("<pre><code>highlighted</code></pre>");
  mockHighlighter.loadLanguage.mockReset().mockResolvedValue(undefined);
  mockHighlighter.loadTheme.mockReset().mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  document.documentElement.classList.remove("dark");
  host.remove();
});

async function renderCode(code: string, props: { language?: "yaml" | "json" | "bash" | "text"; theme?: "dark" | "light" } = {}) {
  await act(async () => {
    root.render(<CodeViewer code={code} {...props} />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function expectThemeTokenSurface() {
  const surface = host.firstElementChild as HTMLElement;
  expect(surface.classList.contains("bg-theme-elevated")).toBe(true);
  expect(surface.classList.contains("text-theme-text-primary")).toBe(true);
  expect(surface.querySelector(".shiki-viewer.with-line-numbers")).not.toBeNull();
  expect(surface.querySelector("style")?.textContent).toContain(".shiki-viewer.with-line-numbers .line::before");
}

it("does not load Shiki for empty code", async () => {
  await renderCode("");

  expect(createHighlighter).not.toHaveBeenCalled();
  expect(mockCodeToHtml).not.toHaveBeenCalled();
  expect(host.querySelector("pre")).toBeNull();
  expectThemeTokenSurface();
});

it("uses the active document theme and theme tokens for readable code", async () => {
  await renderCode("kind: ServiceAccount");
  expect(mockCodeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-light",
  });
  expect(createHighlighter).toHaveBeenCalledWith(expect.objectContaining({
    langs: [],
    themes: [],
    engine: expect.anything(),
  }));
  expect(mockHighlighter.loadLanguage).toHaveBeenLastCalledWith("yaml");
  expect(mockHighlighter.loadTheme).toHaveBeenLastCalledWith("github-light");
  expectThemeTokenSurface();

  mockCodeToHtml.mockClear();
  await act(async () => {
    document.documentElement.classList.add("dark");
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(mockCodeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-dark",
  });
  expect(mockHighlighter.loadTheme).toHaveBeenLastCalledWith("github-dark");
  expectThemeTokenSurface();
});

it("applies explicit theme overrides to both highlighting and the surface", async () => {
  document.documentElement.classList.add("dark");
  await renderCode("kind: ServiceAccount", { theme: "light" });

  let surface = host.firstElementChild as HTMLElement;
  expect(mockCodeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-light",
  });
  expect(surface.style.colorScheme).toBe("light");
  expectThemeTokenSurface();

  await act(async () => {
    document.documentElement.classList.remove("dark");
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await renderCode("kind: ServiceAccount", { theme: "dark" });

  surface = host.firstElementChild as HTMLElement;
  expect(mockCodeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-dark",
  });
  expect(surface.style.colorScheme).toBe("dark");
  expectThemeTokenSurface();
});

it("keeps fallback code readable when syntax highlighting fails", async () => {
  const logError = vi.spyOn(console, "error").mockImplementation(() => {});
  mockCodeToHtml.mockRejectedValue(new Error("highlighting unavailable"));

  try {
    await renderCode("kind: ServiceAccount");

    expect(host.textContent).toContain("kind: ServiceAccount");
    expectThemeTokenSurface();
  } finally {
    logError.mockRestore();
  }
});
