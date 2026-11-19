import { describe, expect, it } from "vitest";
import {
  splitStory,
  storyHasPlacements,
  storyPlainText,
  storyReferenceHref,
  storyReferenceIndex,
  UNRESOLVED_STORY_INDEX,
} from "./investigationStory";

describe("splitStory", () => {
  it("places a marker on its own line and references one inside a sentence", () => {
    const { segments } = splitStory(
      "The pod crashes.\n\n[[kubeatlas:evidence=0]]\n\nAs [[kubeatlas:evidence=0]] shows, auth fails.\n  [[kubeatlas:evidence=2]]  \nDone.",
    );
    expect(segments).toEqual([
      { kind: "prose", markdown: "The pod crashes." },
      { kind: "placement", index: 0 },
      {
        kind: "prose",
        markdown:
          "As [[[kubeatlas:evidence=0]]](#kubeatlas-evidence-0) shows, auth fails.",
      },
      { kind: "placement", index: 2 },
      { kind: "prose", markdown: "Done." },
    ]);
    expect(JSON.stringify(segments)).toContain("#kubeatlas-evidence-0)");
  });

  it("treats markers inside fences, inline code and blockquotes as literal text", () => {
    const report = [
      "Quoted output:",
      "```",
      "[[kubeatlas:evidence=1]]",
      "```",
      "Inline `[[kubeatlas:evidence=1]]` is code, but [[kubeatlas:evidence=3]] is not.",
      "> [[kubeatlas:evidence=4]]",
      "~~~txt",
      "[[kubeatlas:evidence=5]]",
      "~~~",
    ].join("\n");
    const { segments } = splitStory(report);
    expect(segments.filter((s) => s.kind === "placement")).toEqual([]);
    expect(JSON.stringify(segments)).toContain("#kubeatlas-evidence-3)");
    const prose = segments
      .map((s) => (s.kind === "prose" ? s.markdown : ""))
      .join("\n");
    expect(prose).toContain("`[[kubeatlas:evidence=1]]`");
    expect(prose).toContain("> [[kubeatlas:evidence=4]]");
    expect(prose).toContain("[[[kubeatlas:evidence=3]]](#kubeatlas-evidence-3)");
  });

  it("keeps indented-code markers and unbalanced closing fences literal", () => {
    const indented = splitStory("Text.\n\n    [[kubeatlas:evidence=0]]");
    expect(indented.segments.every((s) => s.kind === "prose")).toBe(true);
    expect(JSON.stringify(indented.segments)).not.toContain("#kubeatlas-evidence");
    expect(
      indented.segments.map((s) => s.kind === "prose" && s.markdown).join("\n"),
    ).toContain("    [[kubeatlas:evidence=0]]");
    const fence = splitStory(
      "```\n````not-a-close\n[[kubeatlas:evidence=0]]\n```\n\n[[kubeatlas:evidence=1]]",
    );
    expect(
      fence.segments
        .filter((s) => s.kind === "placement")
        .map((s) => s.kind === "placement" && s.index),
    ).toEqual([1]);
  });

  it("resolves a ref-form marker to its item, and flags one nothing cites", () => {
    const ref = "ev_" + "a".repeat(26) + "_" + "b".repeat(26);
    const stray = "ev_" + "c".repeat(26) + "_" + "d".repeat(26);
    const resolve = (candidate: string) => (candidate === ref ? 2 : undefined);
    const { segments } = splitStory(
      `The log shows it:\n\n[[kubeatlas:evidence-ref=${ref}]]\n\nSee [[kubeatlas:evidence-ref=${ref}|compact]] and [[kubeatlas:evidence-ref=${stray}]].`,
      resolve,
    );
    expect(segments[1]).toEqual({ kind: "placement", index: 2 });
    expect(JSON.stringify(segments)).toContain("#kubeatlas-evidence-2)");
    expect(JSON.stringify(segments)).toContain("#kubeatlas-evidence--1)");
    expect(segments[2]).toMatchObject({ kind: "prose" });
    const inline = (segments[2] as { markdown: string }).markdown;
    expect(inline).toContain("](#kubeatlas-evidence-2)");
    expect(inline).toContain(`](#kubeatlas-evidence-${UNRESOLVED_STORY_INDEX})`);
    expect(storyPlainText(`x [[kubeatlas:evidence-ref=${ref}]] y`)).toBe("x  y");
  });
  it("keeps a marker inside a double-backtick code span literal", () => {
    const { segments } = splitStory(
      "Write `` [[kubeatlas:evidence=0]] `` to place, then see [[kubeatlas:evidence=0]].",
    );
    expect(JSON.stringify(segments)).toContain("#kubeatlas-evidence-0)");
    expect((segments[0] as { markdown: string }).markdown).toContain(
      "`` [[kubeatlas:evidence=0]] ``",
    );
  });

  it("keeps a marker literal inside a code span that runs across lines", () => {
    const split = splitStory(
      "Quoted `example\n[[kubeatlas:evidence=0]]\nend` here.\n\n[[kubeatlas:evidence=0]]",
    );
    expect(split.segments.map((segment) => segment.kind)).toEqual([
      "prose",
      "placement",
    ]);
    expect(JSON.stringify(split.segments)).not.toContain("#kubeatlas-evidence");
    expect((split.segments[0] as { markdown: string }).markdown).toContain(
      "[[kubeatlas:evidence=0]]\nend`",
    );
  });

  it("closes an open span at a fence, at a whitespace-only line, and keeps tab-indented lines literal", () => {
    const kinds = (report: string) =>
      splitStory(report).segments.map((segment) => segment.kind);
    expect(kinds("Quoted `example\n~~~\nx\n~~~\n[[kubeatlas:evidence=0]]")).toEqual(
      ["prose", "placement"],
    );
    expect(kinds("Quoted `example\n    \n[[kubeatlas:evidence=0]]")).toEqual([
      "prose",
      "placement",
    ]);
    // A tab-indented fence is indented code, so the marker between two of
    // them stands on its own line in prose.
    expect(kinds("\t~~~\n[[kubeatlas:evidence=0]]\n\t~~~")).toEqual([
      "prose",
      "placement",
      "prose",
    ]);
    expect(kinds("\t[[kubeatlas:evidence=0]]")).toEqual(["prose"]);
    expect(
      JSON.stringify(splitStory("\t[[kubeatlas:evidence=0]]").segments),
    ).not.toContain("#kubeatlas-evidence");
  });

  it("reads the compact variant as a placement flag", () => {
    const { segments } = splitStory(
      "A.\n[[kubeatlas:evidence=2|compact]]\nB [[kubeatlas:evidence=2|compact]].",
    );
    expect(segments[1]).toEqual({ kind: "placement", index: 2, compact: true });
    expect(segments[2]).toEqual({
      kind: "prose",
      markdown: "B [[[kubeatlas:evidence=2|compact]]](#kubeatlas-evidence-2).",
    });
  });

  it("keeps an unterminated fence literal to the end", () => {
    const { segments } = splitStory("```\n[[kubeatlas:evidence=0]]\nstill code");
    expect(segments).toEqual([
      { kind: "prose", markdown: "```\n[[kubeatlas:evidence=0]]\nstill code" },
    ]);
  });

  it("round-trips reference hrefs and strips markers for plain text", () => {
    expect(storyReferenceIndex(storyReferenceHref(7))).toBe(7);
    expect(storyReferenceIndex("#other")).toBeUndefined();
    expect(
      storyPlainText("A.\n\n[[kubeatlas:evidence=0]]\n\nB [[kubeatlas:evidence=1]]."),
    ).toBe("A.\n\nB .");
    expect(storyHasPlacements("no markers")).toBe(false);
    expect(storyHasPlacements("x [[kubeatlas:evidence=0]]")).toBe(true);
  });
});
