// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
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

function readThemeVariables(): string {
  let directory = process.cwd();
  while (true) {
    const path = join(directory, "packages/k8s-ui/src/theme/variables.css");
    if (existsSync(path)) return readFileSync(path, "utf8");
    const parent = dirname(directory);
    if (parent === directory) throw new Error("Could not locate the theme token stylesheet");
    directory = parent;
  }
}

const themeVariables = readThemeVariables();

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
  const surface = expectThemeSurface();
  expect(surface.querySelector(".shiki-viewer.with-line-numbers")).not.toBeNull();
  const styles = surface.querySelector("style")?.textContent ?? "";
  expect(styles).toContain(".shiki-viewer.with-line-numbers .line::before");
  expect(styles).toContain("color: var(--text-tertiary);");
  expect(styles).not.toContain("background: transparent !important;");
}

function expectThemeSurface() {
  const surface = host.firstElementChild as HTMLElement;
  expect(surface.classList.contains("bg-theme-surface")).toBe(true);
  expect(surface.classList.contains("text-theme-text-primary")).toBe(true);
  return surface;
}

function themeTokenPair(name: string): [string, string] {
  const match = themeVariables.match(
    new RegExp(`--${name}:\\s*light-dark\\(\\s*(#[0-9a-f]{6})\\s*,\\s*(#[0-9a-f]{6})\\s*\\)`, "i"),
  );
  if (!match) throw new Error(`Missing light-dark color token: ${name}`);
  return [match[1], match[2]];
}

function relativeLuminance(color: string): number {
  const channel = (offset: number) => {
    const value = Number.parseInt(color.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5);
}

function contrastRatio(foreground: string, background: string): number {
  const [high, low] = [relativeLuminance(foreground), relativeLuminance(background)].sort((a, b) => b - a);
  return (high + 0.05) / (low + 0.05);
}

it("does not load Shiki for empty code", async () => {
  await renderCode("");

  expect(createHighlighter).not.toHaveBeenCalled();
  expect(mockCodeToHtml).not.toHaveBeenCalled();
  expect(host.querySelector("pre")).toBeNull();
  expectThemeTokenSurface();
});

it("keeps code, gutter, loading, and fallback text above 4.5:1 using the actual theme tokens", () => {
  const [lightSurface, darkSurface] = themeTokenPair("bg-surface");
  const [lightPrimary, darkPrimary] = themeTokenPair("text-primary");
  const [lightSecondary, darkSecondary] = themeTokenPair("text-secondary");
  const [lightTertiary, darkTertiary] = themeTokenPair("text-tertiary");
  const themeCases = [
    {
      surface: lightSurface,
      primary: lightPrimary,
      secondary: lightSecondary,
      gutter: lightTertiary,
      shikiSurface: "#FFFFFF",
    },
    {
      surface: darkSurface,
      primary: darkPrimary,
      secondary: darkSecondary,
      gutter: darkTertiary,
      shikiSurface: "#24292E",
    },
  ];

  for (const theme of themeCases) {
    expect(contrastRatio(theme.primary, theme.surface)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(theme.secondary, theme.surface)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(theme.gutter, theme.shikiSurface)).toBeGreaterThanOrEqual(4.5);
  }
  expect(contrastRatio("#22863A", "#FFFFFF")).toBeGreaterThanOrEqual(4.5);
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

it("uses secondary theme text while highlighting is pending", async () => {
  document.documentElement.classList.add("dark");
  let resolveCode: ((html: string) => void) | undefined;
  mockCodeToHtml.mockImplementation(() => new Promise((resolve) => { resolveCode = resolve; }));

  await renderCode("kind: ServiceAccount", { theme: "light" });

  const surface = expectThemeSurface();
  expect(surface.style.colorScheme).toBe("light");
  expect(host.querySelector(".text-theme-text-secondary")?.textContent).toBe("Loading…");

  await act(async () => {
    if (!resolveCode) throw new Error("Code highlighting did not start");
    resolveCode("<pre><code>highlighted</code></pre>");
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
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
  document.documentElement.classList.add("dark");
  const logError = vi.spyOn(console, "error").mockImplementation(() => {});
  mockCodeToHtml.mockRejectedValue(new Error("highlighting unavailable"));

  try {
    await renderCode("kind: ServiceAccount", { theme: "light" });

    expect(host.textContent).toContain("kind: ServiceAccount");
    expect((host.firstElementChild as HTMLElement).style.colorScheme).toBe("light");
    expectThemeTokenSurface();

    await renderCode("kind: ServiceAccount", { theme: "dark" });

    expect(host.textContent).toContain("kind: ServiceAccount");
    expect((host.firstElementChild as HTMLElement).style.colorScheme).toBe("dark");
    expectThemeTokenSurface();
  } finally {
    logError.mockRestore();
  }
});
