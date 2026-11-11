// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { codeToHtml } from "shiki";
import { CodeViewer } from "./CodeViewer";

vi.mock("shiki", () => ({
  codeToHtml: vi.fn(),
}));

vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);

let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  document.documentElement.classList.remove("dark");
  vi.mocked(codeToHtml).mockReset().mockResolvedValue("<pre><code>highlighted</code></pre>");
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  document.documentElement.classList.remove("dark");
  host.remove();
});

async function renderCode(code: string) {
  await act(async () => {
    root.render(<CodeViewer code={code} />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function expectThemeTokenSurface() {
  const surface = host.firstElementChild as HTMLElement;
  expect(surface.classList.contains("bg-theme-elevated")).toBe(true);
  expect(surface.classList.contains("text-theme-text-primary")).toBe(true);
}

it("uses the active document theme and theme tokens for readable code", async () => {
  await renderCode("kind: ServiceAccount");
  expect(codeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-light",
  });
  expectThemeTokenSurface();

  vi.mocked(codeToHtml).mockClear();
  await act(async () => {
    document.documentElement.classList.add("dark");
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(codeToHtml).toHaveBeenLastCalledWith("kind: ServiceAccount", {
    lang: "yaml",
    theme: "github-dark",
  });
  expectThemeTokenSurface();
});

it("keeps the themed surface for empty code without invoking the highlighter", async () => {
  await renderCode("");

  expect(codeToHtml).not.toHaveBeenCalled();
  expect(host.querySelector("pre")).toBeNull();
  expectThemeTokenSurface();
});

it("keeps fallback code readable when syntax highlighting fails", async () => {
  const logError = vi.spyOn(console, "error").mockImplementation(() => {});
  vi.mocked(codeToHtml).mockRejectedValue(new Error("highlighting unavailable"));

  try {
    await renderCode("kind: ServiceAccount");

    expect(host.textContent).toContain("kind: ServiceAccount");
    expectThemeTokenSurface();
  } finally {
    logError.mockRestore();
  }
});
