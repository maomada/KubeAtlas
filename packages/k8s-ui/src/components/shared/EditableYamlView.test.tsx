// @vitest-environment jsdom
//
// Applying a reviewed edit can be refused because the resource changed after
// the review. The review then refreshes against the latest version and says
// so, once. That notice belongs to one attempt: a retry that fails for another
// reason shows its own error.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resourceToYaml } from "../../utils/yaml";
import type { YamlEditorProps } from "../ui/YamlEditor";

vi.mock("../ui/YamlEditor", async () => {
  const { useEffect } = await import("react");
  return {
    YamlEditor: ({
      value,
      onChange,
      schemaLoader,
    }: Pick<YamlEditorProps, "value" | "onChange" | "schemaLoader">) => (
      <>
        <textarea
          data-testid="yaml-editor-value"
          value={value}
          onChange={(event) => onChange?.(event.target.value)}
        />
        {schemaLoader && (
          <button type="button" onClick={() => { void schemaLoader([]); }}>
            Load schemas
          </button>
        )}
      </>
    ),
    YamlDiffEditor: ({
      onReadyChange,
    }: {
      onReadyChange?: (ready: boolean) => void;
    }) => {
      useEffect(() => onReadyChange?.(true), [onReadyChange]);
      return <div />;
    },
  };
});

const { EditableYamlView } = await import("./EditableYamlView");
const { YamlDraftPolicyProvider } = await import("./YamlDraftPolicy");
const { yamlDraftStorageKey } = await import("./yamlDraftStorage");

vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
vi.stubGlobal(
  "ResizeObserver",
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);
let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const resource = { kind: "ConfigMap", namespace: "shop", name: "app" };
const data = {
  apiVersion: "v1",
  kind: "ConfigMap",
  metadata: { name: "app", namespace: "shop" },
  data: { a: "1" },
};
const reviewed = (rv: string) => ({
  documents: [
    {
      index: 0,
      kind: "ConfigMap",
      name: "app",
      namespace: "shop",
      status: "accepted" as const,
      action: "update" as const,
      reviewedResourceVersion: rv,
    },
  ],
  nonAtomic: false,
});

function render(props: {
  onSave: () => Promise<void>;
  onPreview: () => Promise<ReturnType<typeof reviewed>>;
  saveError?: string | null;
}) {
  act(() =>
    root.render(
      <EditableYamlView
        resource={resource as never}
        data={data}
        onCopy={() => {}}
        copied={false}
        onSave={props.onSave}
        onPreview={props.onPreview}
        saveError={props.saveError ?? null}
      />,
    ),
  );
}

function renderWithPolicy(
  props: { onSave: () => Promise<void>; onPreview: () => Promise<ReturnType<typeof reviewed>>; schemaLoader?: () => unknown; onSaved?: () => void; onDuplicate?: (params: { kind: string; namespace: string; name: string; yaml: string }) => void },
  scope?: string,
  readOnly = false,
  leafReadOnly = false,
) {
  act(() => root.render(
    <YamlDraftPolicyProvider scope={scope} readOnly={readOnly}>
      <EditableYamlView
        resource={resource as never}
        data={data}
        onCopy={() => {}}
        copied={false}
        readOnly={leafReadOnly}
        onSave={props.onSave}
        onPreview={props.onPreview}
        schemaLoader={props.schemaLoader as never}
        onSaved={props.onSaved}
        onDuplicate={props.onDuplicate}
      />
    </YamlDraftPolicyProvider>,
  ));
}

const click = async (label: RegExp) => {
  const b = [...document.querySelectorAll("button")].find((x) =>
    label.test(x.textContent ?? ""),
  );
  if (!b) throw new Error(`no button ${label} in ${document.body.textContent}`);
  await act(async () => {
    b.click();
    await new Promise((r) => setTimeout(r, 0));
  });
};
const text = () => document.body.textContent ?? "";

it('keeps Duplicate available to writable owners and hides it for Viewer or leaf read-only mode', () => {
  const onDuplicate = vi.fn()
  const onSave = vi.fn(async () => {})
  const onPreview = vi.fn(async () => reviewed('1'))
  const props = { onSave, onPreview, onDuplicate }
  renderWithPolicy(props, 'org-a:owner:logical-cluster-a')
  const duplicate = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Duplicate')
  expect(duplicate).not.toBeUndefined()
  act(() => duplicate!.click())
  expect(onDuplicate).toHaveBeenCalledWith(expect.objectContaining({
    kind: 'ConfigMap',
    namespace: 'shop',
    name: 'app',
    yaml: expect.any(String),
  }))

  renderWithPolicy(props, 'org-b:viewer:logical-cluster-b', true)
  expect([...host.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Duplicate')).toBe(false)
  expect(onDuplicate).toHaveBeenCalledTimes(1)

  renderWithPolicy(props, 'org-a:owner:logical-cluster-a', false, true)
  expect([...host.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Duplicate')).toBe(false)
  expect(onDuplicate).toHaveBeenCalledTimes(1)
})

it("says the resource changed after review, and only for the attempt that found it", async () => {
  const onPreview = vi
    .fn()
    .mockResolvedValueOnce(reviewed("1")) // the review
    .mockResolvedValueOnce(reviewed("2")) // refresh after the conflict: a newer version
    .mockRejectedValueOnce(new Error("preview unavailable")); // refresh after the second failure
  const onSave = vi
    .fn()
    .mockRejectedValueOnce(new Error("conflict"))
    .mockRejectedValueOnce(new Error("forbidden"));
  render({ onSave, onPreview });

  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  render({
    onSave,
    onPreview,
    saveError:
      "resource changed after review; review the latest version before applying",
  });
  expect(text()).toContain("This resource changed after your review");
  expect(text()).not.toContain("review the latest version before applying");

  await click(/Apply reviewed changes/);
  render({
    onSave,
    onPreview,
    saveError: "forbidden: cannot patch configmaps",
  });
  expect(text()).not.toContain("This resource changed after your review");
  expect(text()).toContain("forbidden: cannot patch configmaps");
  expect(onSave).toHaveBeenCalledTimes(2);
  expect(onPreview).toHaveBeenCalledTimes(3);
});

it("says nothing extra when the refreshed review found the same version", async () => {
  const onPreview = vi.fn().mockResolvedValue(reviewed("1"));
  const onSave = vi.fn().mockRejectedValueOnce(new Error("webhook denied"));
  render({ onSave, onPreview });
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  render({
    onSave,
    onPreview,
    saveError: "admission webhook denied the request",
  });
  expect(text()).not.toContain("This resource changed after your review");
  expect(text()).toContain("admission webhook denied the request");
  expect(onPreview).toHaveBeenCalledTimes(2);
});

it("doesn't call a deleted resource changed", async () => {
  const gone = {
    documents: [
      {
        index: 0,
        kind: "ConfigMap",
        name: "app",
        namespace: "shop",
        status: "rejected" as const,
        message: 'configmaps "app" not found',
      },
    ],
    nonAtomic: false,
  };
  const onPreview = vi
    .fn()
    .mockResolvedValueOnce(reviewed("1"))
    .mockResolvedValueOnce(gone);
  const onSave = vi.fn().mockRejectedValueOnce(new Error("not found"));
  render({ onSave, onPreview });
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  render({ onSave, onPreview, saveError: 'configmaps "app" not found' });
  expect(text()).not.toContain("This resource changed after your review");
  expect(text()).toContain('configmaps "app" not found');
});

it("doesn't call a switched cluster a changed resource", async () => {
  const onPreview = vi
    .fn()
    .mockResolvedValueOnce({ ...reviewed("1"), context: "kind-a" })
    .mockResolvedValueOnce({ ...reviewed("2"), context: "kind-b" });
  const onSave = vi.fn().mockRejectedValueOnce(new Error("cluster changed"));
  render({ onSave, onPreview });
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  render({
    onSave,
    onPreview,
    saveError: "the active cluster changed after review",
  });
  expect(text()).not.toContain("This resource changed after your review");
  expect(text()).toContain("the active cluster changed after review");
});

it("ignores an earlier attempt's refresh that lands after a newer attempt", async () => {
  let finishFirstRefresh: (v: ReturnType<typeof reviewed>) => void = () => {};
  const onPreview = vi
    .fn()
    .mockResolvedValueOnce(reviewed("1"))
    .mockReturnValueOnce(
      new Promise<ReturnType<typeof reviewed>>((r) => {
        finishFirstRefresh = r;
      }),
    )
    .mockRejectedValueOnce(new Error("preview unavailable"));
  const onSave = vi
    .fn()
    .mockRejectedValueOnce(new Error("conflict"))
    .mockRejectedValueOnce(new Error("forbidden"));
  render({ onSave, onPreview });
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  await click(/Apply reviewed changes/);
  await act(async () => {
    finishFirstRefresh(reviewed("2"));
    await new Promise((r) => setTimeout(r, 0));
  });
  render({
    onSave,
    onPreview,
    saveError: "forbidden: cannot patch configmaps",
  });
  expect(text()).not.toContain("This resource changed after your review");
  expect(text()).toContain("forbidden: cannot patch configmaps");
});

it("drops a refresh from an apply made before going back to a newer review", async () => {
  let finishFirstRefresh: (v: ReturnType<typeof reviewed>) => void = () => {};
  const onPreview = vi
    .fn()
    .mockResolvedValueOnce(reviewed("1"))
    .mockReturnValueOnce(
      new Promise<ReturnType<typeof reviewed>>((r) => {
        finishFirstRefresh = r;
      }),
    )
    .mockResolvedValueOnce(reviewed("3"));
  const onSave = vi
    .fn()
    .mockRejectedValueOnce(new Error("conflict"))
    .mockResolvedValueOnce(undefined);
  render({ onSave, onPreview });
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply reviewed changes/);
  await click(/Back to edit/);
  await click(/Review changes/);
  await act(async () => {
    finishFirstRefresh(reviewed("2"));
    await new Promise((r) => setTimeout(r, 0));
  });
  expect(text()).not.toContain("This resource changed after your review");
  await click(/Apply reviewed changes/);
  expect(onSave).toHaveBeenLastCalledWith(
    expect.objectContaining({ reviewedResourceVersion: "3" }),
  );
});

it("uses the accent token for a generic ConfigMap Edit control", () => {
  render({ onSave: async () => {}, onPreview: async () => reviewed("1") });
  const edit = [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Edit")!;
  expect(edit.classList.contains("text-accent-text")).toBe(true);
  expect(edit.className).not.toContain("text-blue-");
});


it("keeps the SSA omission caveat in a direct editor save with a known paused baseline", () => {
  const target = { kind: "TaskBucket", namespace: "shop", name: "archive" };
  const baseline = { apiVersion: "example.test/v1", kind: "TaskBucket", metadata: { name: "archive", namespace: "shop", annotations: { "crossplane.io/paused": "true" } }, spec: { providerConfigRef: { name: "default" } } };
  sessionStorage.setItem("radar_yaml_draft:TaskBucket/shop/archive", "apiVersion: example.test/v1\nkind: TaskBucket\nmetadata:\n  name: archive\n  namespace: shop\nspec:\n  providerConfigRef:\n    name: default\n");
  act(() => root.render(<EditableYamlView resource={target as never} data={baseline} onCopy={() => {}} copied={false} onSave={async () => {}} />));
  expect(text()).toContain("Pause affects reconciliation");
  expect(text()).toContain("Omitting the annotation from server-side apply YAML does not guarantee its removal");
});

it("does not carry a paused baseline to a different API group in a direct editor save", async () => {
  const target = { kind: "TaskBucket", namespace: "shop", name: "archive" };
  const baseline = { apiVersion: "example.test/v1", kind: "TaskBucket", metadata: { name: "archive", namespace: "shop", annotations: { "crossplane.io/paused": "true" } }, spec: { providerConfigRef: { name: "default" } } };
  const yaml = "apiVersion: another.example.test/v1\nkind: TaskBucket\nmetadata:\n  name: archive\n  namespace: shop\nspec: {}\n";
  const onSave = vi.fn(async () => {});
  sessionStorage.setItem("radar_yaml_draft:TaskBucket/shop/archive", yaml);
  act(() => root.render(<EditableYamlView resource={target as never} data={baseline} onCopy={() => {}} copied={false} onSave={onSave} />));
  expect(text()).not.toContain("Pause affects reconciliation");
  expect(text()).not.toContain("Existing pause state is unknown");
  await click(/^Save$/);
  expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ kind: "TaskBucket", namespace: "shop", name: "archive", yaml }));
});

it("does not infer annotation omission before the first direct-edit debounce settles", () => {
  vi.useFakeTimers();
  try {
    const target = { kind: "TaskBucket", namespace: "shop", name: "archive" };
    const baseline = { apiVersion: "example.test/v1", kind: "TaskBucket", metadata: { name: "archive", namespace: "shop", annotations: { "crossplane.io/paused": "true" } }, spec: { providerConfigRef: { name: "default" } } };
    act(() => root.render(<EditableYamlView resource={target as never} data={baseline} onCopy={() => {}} copied={false} onSave={async () => {}} />));
    const edit = [...host.querySelectorAll("button")].find(button => button.textContent?.trim() === "Edit")!;
    act(() => edit.click());
    expect(text()).not.toContain("Omitting the annotation");
    act(() => vi.advanceTimersByTime(200));
    expect(text()).toContain("Pause affects reconciliation");
    expect(text()).not.toContain("Omitting the annotation");
  } finally {
    vi.useRealTimers();
  }
});


it("retains raw acknowledged ref-less pause evidence through editor and status-free review", async () => {
  const target = { kind: "TaskClaim", namespace: "shop", name: "ref-less" };
  const raw = { apiVersion: "example.test/v1", kind: "TaskClaim", metadata: { name: "ref-less", namespace: "shop", annotations: { "crossplane.io/paused": "true" } }, spec: {}, status: { conditions: [{ type: "Synced", status: "False", reason: "ReconcilePaused" }] } };
  const clean = resourceToYaml(raw);
  expect(clean).not.toContain("status:");
  expect(clean).not.toContain("ReconcilePaused");
  const onPreview = vi.fn(async () => ({ documents: [{ index: 0, status: "accepted" as const, action: "update" as const, baselineYaml: clean, predictedYaml: clean, reviewedResourceVersion: "1" }], nonAtomic: false }));
  act(() => root.render(<EditableYamlView resource={target as never} data={raw} onCopy={() => {}} copied={false} onSave={async () => {}} onPreview={onPreview} />));
  await click(/^Edit$/);
  await click(/Review changes/);
  expect(text()).toContain("Pause affects reconciliation");
  expect(text()).toContain("Server preview keeps the pause request on TaskClaim shop/ref-less");
  expect(onPreview).toHaveBeenCalledWith(expect.objectContaining({ yaml: clean }));
  expect(raw.status.conditions[0].reason).toBe("ReconcilePaused");
});

it("keeps standalone recovery while ignoring legacy drafts in scoped Hub mode", () => {
  const draft = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  owner: unsaved\n";
  const onSave = vi.fn(async () => {});
  const onPreview = vi.fn(async () => reviewed("1"));
  sessionStorage.setItem("radar_yaml_draft:ConfigMap/shop/app", draft);
  render({ onSave, onPreview });
  expect(text()).toContain("Editing Resource");
  expect(text()).toContain("Review changes");

  sessionStorage.clear();
  sessionStorage.setItem("radar_yaml_draft:ConfigMap/shop/app", draft);
  renderWithPolicy({ onSave, onPreview }, "hub-owner-a-cluster-a");
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect(onPreview).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
  expect(sessionStorage.getItem("radar_yaml_draft:ConfigMap/shop/app")).toBe(draft);
});

it("restores a scoped draft after a same-scope remount and ignores it in another scope", () => {
  const draft = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  owner: unsaved\n";
  const scope = "owner-a:cluster-a";
  const otherScope = "owner-a:cluster-b";
  const draftKey = yamlDraftStorageKey(scope, resource);
  const onSave = vi.fn(async () => {});
  const onPreview = vi.fn(async () => reviewed("1"));
  const editorYaml = () => host.querySelector<HTMLTextAreaElement>('[data-testid="yaml-editor-value"]')?.value;
  sessionStorage.setItem(draftKey, draft);

  renderWithPolicy({ onSave, onPreview }, scope);
  expect(text()).toContain("Editing Resource");
  expect(editorYaml()).toBe(draft);
  expect(sessionStorage.getItem(draftKey)).toBe(draft);

  act(() => root.unmount());
  root = createRoot(host);
  renderWithPolicy({ onSave, onPreview }, scope);
  expect(text()).toContain("Editing Resource");
  expect(editorYaml()).toBe(draft);
  expect(sessionStorage.getItem(draftKey)).toBe(draft);

  act(() => root.unmount());
  root = createRoot(host);
  renderWithPolicy({ onSave, onPreview }, otherScope);
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect(editorYaml()).toBeUndefined();
  expect(sessionStorage.getItem(draftKey)).toBe(draft);
  expect(sessionStorage.getItem(yamlDraftStorageKey(otherScope, resource))).toBeNull();
  expect(onPreview).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it("keeps a leaf read-only policy additive to a writable host policy", () => {
  const draft = "owner-unsaved";
  sessionStorage.setItem("radar_yaml_draft:ConfigMap/shop/app", draft);
  sessionStorage.setItem(yamlDraftStorageKey("library-scope", resource), draft);
  act(() => root.render(
    <YamlDraftPolicyProvider scope="library-scope" readOnly={false}>
      <EditableYamlView resource={resource as never} data={data} onCopy={() => {}} copied={false} readOnly />
    </YamlDraftPolicyProvider>,
  ));
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect([...host.querySelectorAll("button")].some(button => button.textContent?.trim() === "Edit")).toBe(false);
  expect(sessionStorage.getItem(yamlDraftStorageKey("library-scope", resource))).toBeNull();
});

it("does not restore an Owner A draft for another owner or a Viewer", () => {
  const onSave = vi.fn(async () => {});
  const onPreview = vi.fn(async () => reviewed("1"));
  const ownerAScope = "org-a:owner:logical-cluster-a";
  sessionStorage.setItem(yamlDraftStorageKey(ownerAScope, resource), "owner-unsaved");

  renderWithPolicy({ onSave, onPreview }, "org-a:owner-b:logical-cluster-a");
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");

  renderWithPolicy({ onSave, onPreview }, "org-b:viewer:logical-cluster-b", true);
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect(text()).not.toContain("Apply reviewed changes");
  expect(onPreview).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it("keeps Viewer mode from invoking schema, preview, or save callbacks", async () => {
  const ownerSchemaLoader = vi.fn(async () => ({ schemas: [] }));
  const ownerScope = "org-a:owner:logical-cluster-a";
  sessionStorage.setItem(yamlDraftStorageKey(ownerScope, resource), "owner-unsaved");
  const onSave = vi.fn(async () => {});
  const onPreview = vi.fn(async () => reviewed("1"));
  renderWithPolicy({ onSave, onPreview, schemaLoader: ownerSchemaLoader }, ownerScope);
  expect(host.querySelector<HTMLTextAreaElement>('[data-testid="yaml-editor-value"]')?.value).toBe("owner-unsaved");
  await click(/Load schemas/);
  expect(ownerSchemaLoader).toHaveBeenCalledTimes(1);

  const schemaLoader = vi.fn(async () => undefined);
  const scope = "org-b:viewer:logical-cluster-b";
  sessionStorage.setItem(yamlDraftStorageKey(scope, resource), "owner-unsaved");
  renderWithPolicy({ onSave, onPreview, schemaLoader }, scope, true);
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect(host.querySelector('[data-testid="yaml-editor-value"]')).toBeNull();
  expect([...host.querySelectorAll("button")].some((button) => button.textContent === "Load schemas")).toBe(false);
  expect(ownerSchemaLoader).toHaveBeenCalledTimes(1);
  expect(schemaLoader).not.toHaveBeenCalled();
  expect(onPreview).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it("remounts when scope changes and ignores a late preview from the previous scope", async () => {
  let finishPreview: (result: ReturnType<typeof reviewed>) => void = () => {};
  const onPreview = vi.fn(() => new Promise<ReturnType<typeof reviewed>>(resolve => { finishPreview = resolve; }));
  const onSave = vi.fn(async () => {});
  renderWithPolicy({ onSave, onPreview }, "owner-a:cluster-a");
  await click(/^Edit$/);
  await click(/Review changes/);
  expect(onPreview).toHaveBeenCalledTimes(1);

  renderWithPolicy({ onSave, onPreview }, "viewer-b:cluster-b", true);
  await act(async () => {
    finishPreview(reviewed("1"));
    await Promise.resolve();
  });
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Review changes");
  expect(text()).not.toContain("Apply reviewed changes");
  expect(onSave).not.toHaveBeenCalled();
});

it("does not notify the next scope after a reviewed save completes late", async () => {
  let finishSave: () => void = () => {};
  const onSave = vi.fn(() => new Promise<void>(resolve => { finishSave = resolve; }));
  const onPreview = vi.fn(async () => reviewed("1"));
  const onSaved = vi.fn();
  renderWithPolicy({ onSave, onPreview, onSaved }, "owner-a:cluster-a");
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply/);
  expect(onSave).toHaveBeenCalledTimes(1);

  renderWithPolicy({ onSave, onPreview, onSaved }, "owner-b:cluster-b");
  vi.useFakeTimers();
  try {
    await act(async () => {
      finishSave();
      await Promise.resolve();
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1100);
    });
    expect(onSaved).not.toHaveBeenCalled();
    expect(text()).not.toContain("Apply reviewed changes");
  } finally {
    vi.useRealTimers();
  }
});

it("does not preview again when a reviewed save fails after the scope changes", async () => {
  let failSave: (error: Error) => void = () => {};
  const onSave = vi.fn(() => new Promise<void>((_resolve, reject) => { failSave = reject; }));
  const onPreview = vi.fn(async () => reviewed("1"));
  renderWithPolicy({ onSave, onPreview }, "owner-a:cluster-a");
  await click(/^Edit$/);
  await click(/Review changes/);
  await click(/Apply/);
  expect(onPreview).toHaveBeenCalledTimes(1);

  renderWithPolicy({ onSave, onPreview }, "viewer-b:cluster-b", true);
  await act(async () => {
    failSave(new Error("save failed"));
    await Promise.resolve();
  });
  expect(onPreview).toHaveBeenCalledTimes(1);
  expect(text()).not.toContain("Editing Resource");
  expect(text()).not.toContain("Apply reviewed changes");
});
