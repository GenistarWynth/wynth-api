# Channel Monitor Waveform Toggle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a monitor history display switch so admins can view either availability status or first-token latency as a waveform line.

**Architecture:** Keep all data client-side using the existing recent monitor records. Add a local React state for the history display mode, render the existing status strip for availability, and render an SVG sparkline waveform for first-token latency without adding a chart dependency.

**Tech Stack:** React 19, TypeScript, Base UI ToggleGroup, Tailwind CSS, Bun tests.

---

### Task 1: Waveform History Mode

**Files:**
- Modify: `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx`
- Modify: `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts`
- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/fr.json`
- Modify: `web/default/src/i18n/locales/ja.json`
- Modify: `web/default/src/i18n/locales/ru.json`
- Modify: `web/default/src/i18n/locales/vi.json`
- Modify: `web/default/src/i18n/locales/zh.json`

- [ ] **Step 1: Write failing source-contract test**

Add a test to `channel-monitor-dialog.test.ts` asserting:

```ts
test('can switch history between availability and first token waveform', () => {
  assert.match(dialogSource, /ToggleGroup/)
  assert.match(dialogSource, /historyViewMode/)
  assert.match(dialogSource, /firstTokenPath/)
  assert.match(dialogSource, /polyline/)
  assert.match(dialogSource, /First token waveform/)
  assert.match(dialogSource, /Availability status/)
})
```

- [ ] **Step 2: Verify red**

Run from `web/default`:

```powershell
bun test src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
```

Expected: FAIL because the dialog has no history mode switch or waveform path.

- [ ] **Step 3: Implement minimal waveform mode**

In `channel-monitor-dialog.tsx`:

- import `Activity` from `lucide-react`;
- import `ToggleGroup` and `ToggleGroupItem`;
- add `type MonitorHistoryViewMode = 'availability' | 'first-token'`;
- add local state `historyViewMode`;
- render a compact `ToggleGroup` next to the "Recent 60 records" heading;
- keep the existing status strip when mode is `availability`;
- render an SVG waveform when mode is `first-token`;
- keep existing Tooltip detail content for each point.

The waveform uses `firstTokenLatencyMS` values only. Positive samples become connected points. Missing, failed, or zero samples remain focusable markers but should not create a misleading line segment.

- [ ] **Step 4: Add i18n keys**

Add translations for:

```json
{
  "Availability status": "...",
  "First token waveform": "..."
}
```

Use Chinese:

```json
{
  "Availability status": "可用性状态",
  "First token waveform": "首 token 波形"
}
```

- [ ] **Step 5: Verify green**

Run:

```powershell
bun test src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
bun run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts web/default/src/i18n/locales
git commit -m "feat: add channel monitor waveform history"
```
