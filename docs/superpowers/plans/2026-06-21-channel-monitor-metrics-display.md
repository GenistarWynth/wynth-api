# Channel Monitor Metrics Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve the existing single-channel monitor dialog so it clearly shows first-token latency, endpoint latency, total latency, token counts, availability, tested model, and recent monitor record details.

**Architecture:** Reuse the existing `ChannelMonitorLog` table and `/api/channel/:id/monitor` endpoint. Add a small backend contract field for the latest tested model, tighten average calculations, and enhance the frontend monitor dialog/history strip using the existing design system and i18n flow.

**Tech Stack:** Go 1.26.4 for backend tests, Gin/GORM models, React 19, TypeScript, Base UI Tooltip, Tailwind CSS, Bun for frontend scripts.

---

## File Structure

- `model/channel_monitor.go`
  - Add `latest_model` to `ChannelMonitorInfo`.
  - Fill it from the latest `ChannelMonitorLog`.
  - Change average total latency to ignore zero samples, matching endpoint and first-token averages.
- `model/channel_monitor_test.go`
  - Assert latest model is attached.
  - Assert zero total latency samples are ignored in averages.
  - Assert no-data channels remain safe.
- `controller/channel_monitor_test.go`
  - Assert monitor detail JSON exposes latest model and all metric fields.
- `web/default/src/features/channels/types.ts`
  - Add `latest_model` to `channelMonitorInfoSchema`.
- `web/default/src/features/channels/lib/channel-monitor.ts`
  - Add model to history bar data.
  - Keep bar height derived from first-token latency, then total latency, then endpoint latency.
- `web/default/src/features/channels/lib/channel-monitor.test.ts`
  - Assert model, timing, token, message fields survive history mapping.
- `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx`
  - Use `info.latest_model` for the header fallback.
  - Replace bare history `title` with themed Tooltip detail content.
  - Add average metric and success-count context without crowding the dialog.
- `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts`
  - Assert themed tooltip usage and Chinese/i18n labels are present.
- `web/default/src/i18n/locales/*.json`
  - Add missing frontend i18n keys through the existing i18n sync flow or direct flat-key edits if sync cannot run.

---

### Task 1: Backend Monitor Detail Contract

**Files:**
- Modify: `model/channel_monitor.go`
- Modify: `model/channel_monitor_test.go`
- Modify: `controller/channel_monitor_test.go`

- [ ] **Step 1: Write failing model tests**

Add assertions to `TestAttachChannelMonitorInfo` in `model/channel_monitor_test.go`:

```go
assert.Equal(t, "gpt-4o-mini", channels[0].MonitorInfo.LatestModel)
```

Extend `TestGetChannelMonitorStatsIncludesTimingBreakdowns` so one success row has `LatencyMS: 0` and the expected average total latency ignores it:

```go
logs := []ChannelMonitorLog{
    {ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 0, EndpointLatencyMS: 0, FirstTokenLatencyMS: 0, CheckedAt: 95},
    {ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 100, EndpointLatencyMS: 20, FirstTokenLatencyMS: 60, CheckedAt: 100},
    {ChannelID: 1, Status: ChannelMonitorStatusDegraded, LatencyMS: 300, EndpointLatencyMS: 40, FirstTokenLatencyMS: 120, CheckedAt: 110},
    {ChannelID: 1, Status: ChannelMonitorStatusFailed, LatencyMS: 0, EndpointLatencyMS: 0, FirstTokenLatencyMS: 0, CheckedAt: 120},
}
```

Expected assertion:

```go
assert.InDelta(t, 200.0, stats[1].AverageLatencyMS, 0.0001)
```

- [ ] **Step 2: Write failing controller test assertion**

In `TestGetChannelMonitorDetail` in `controller/channel_monitor_test.go`, assert:

```go
assert.Equal(t, "gpt-4o-mini", response.Data.Info.LatestModel)
assert.Equal(t, 4, response.Data.Info.LatestPromptTokens)
assert.Equal(t, 14, response.Data.Info.LatestCompletionTokens)
```

- [ ] **Step 3: Run focused backend tests and verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller -run "TestAttachChannelMonitorInfo|TestGetChannelMonitorStatsIncludesTimingBreakdowns|TestGetChannelMonitorDetail" -count=1
```

Expected: FAIL because `LatestModel` is not defined and average total latency still includes zero samples.

- [ ] **Step 4: Implement backend contract**

In `model/channel_monitor.go`, add:

```go
LatestModel string `json:"latest_model,omitempty"`
```

to `ChannelMonitorInfo` near the other latest fields.

Set it in `AttachChannelMonitorInfo`:

```go
info.LatestModel = log.Model
```

Change the total average SQL expression in `GetChannelMonitorStats` from:

```go
AVG(latency_ms) AS average_latency_ms
```

to:

```go
AVG(CASE WHEN latency_ms > 0 THEN latency_ms ELSE NULL END) AS average_latency_ms
```

- [ ] **Step 5: Run focused backend tests and verify pass**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller -run "TestAttachChannelMonitorInfo|TestGetChannelMonitorStatsIncludesTimingBreakdowns|TestGetChannelMonitorDetail" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add model/channel_monitor.go model/channel_monitor_test.go controller/channel_monitor_test.go
git commit -m "feat: expose latest channel monitor model"
```

---

### Task 2: Frontend Monitor Data Mapping

**Files:**
- Modify: `web/default/src/features/channels/types.ts`
- Modify: `web/default/src/features/channels/lib/channel-monitor.ts`
- Modify: `web/default/src/features/channels/lib/channel-monitor.test.ts`

- [ ] **Step 1: Write failing TypeScript data tests**

In `web/default/src/features/channels/lib/channel-monitor.test.ts`, extend the first record fixture with:

```ts
model: 'gpt-4o-mini',
```

and assert:

```ts
assert.equal(bars.at(-3)?.model, 'gpt-4o-mini')
assert.equal(bars.at(-3)?.firstTokenLatencyMS, 500)
assert.equal(bars.at(-3)?.promptTokens, 92)
assert.equal(bars.at(-3)?.completionTokens, 156)
assert.equal(bars.at(-3)?.message, 'ok')
```

Also add a schema assertion in the same test file or a focused type test:

```ts
import { channelMonitorInfoSchema } from '../types'

test('monitor info schema accepts latest model', () => {
  const parsed = channelMonitorInfoSchema.parse({
    enabled: true,
    interval_minutes: 10,
    latest_model: 'gpt-4o-mini',
  })
  assert.equal(parsed.latest_model, 'gpt-4o-mini')
})
```

- [ ] **Step 2: Run focused frontend test and verify failure**

Run from `web/default`:

```powershell
bun test src/features/channels/lib/channel-monitor.test.ts
```

Expected: FAIL because `MonitorHistoryBar` has no `model` property and the schema has no `latest_model`.

- [ ] **Step 3: Implement data mapping**

In `web/default/src/features/channels/types.ts`, add:

```ts
latest_model: z.string().optional(),
```

to `channelMonitorInfoSchema`.

In `web/default/src/features/channels/lib/channel-monitor.ts`, add:

```ts
model: string
```

to `MonitorHistoryBar`, set it from:

```ts
model: record.model ?? '',
```

and set empty bars to:

```ts
model: '',
```

- [ ] **Step 4: Run focused frontend test and verify pass**

Run from `web/default`:

```powershell
bun test src/features/channels/lib/channel-monitor.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add web/default/src/features/channels/types.ts web/default/src/features/channels/lib/channel-monitor.ts web/default/src/features/channels/lib/channel-monitor.test.ts
git commit -m "feat: map channel monitor record metrics"
```

---

### Task 3: Themed Monitor History Details

**Files:**
- Modify: `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx`
- Modify: `web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts`

- [ ] **Step 1: Write failing source-contract test**

In `channel-monitor-dialog.test.ts`, add a test that checks the dialog uses the project Tooltip and no longer relies on bare `title={historyTitle(...)}` for monitor details:

```ts
test('renders monitor history details with themed tooltip content', () => {
  assert.match(dialogSource, /TooltipProvider/)
  assert.match(dialogSource, /TooltipTrigger/)
  assert.match(dialogSource, /TooltipContent/)
  assert.doesNotMatch(dialogSource, /title=\{historyTitle\(bar, t\)\}/)
  assert.match(dialogSource, /bar\.model/)
  assert.match(dialogSource, /Average first token latency/)
  assert.match(dialogSource, /Successful checks/)
})
```

- [ ] **Step 2: Run focused dialog test and verify failure**

Run from `web/default`:

```powershell
bun test src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
```

Expected: FAIL because the dialog still uses a bare `title` attribute and does not import Tooltip components.

- [ ] **Step 3: Implement tooltip detail component**

In `channel-monitor-dialog.tsx`, import:

```ts
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
```

Replace `historyTitle` with a detail component:

```tsx
function MonitorHistoryDetails({ bar }: { bar: MonitorHistoryBar }) {
  const { t } = useTranslation()
  if (bar.status === 'empty') return <span>{t('No data')}</span>
  const checkedAt =
    bar.checkedAt > 0 ? formatRelativeTime(bar.checkedAt) : t('No data')
  const rows = [
    [t('Status'), monitorStatusText(bar.status, t)],
    [t('Time'), checkedAt],
    [t('Model'), bar.model || t('No data')],
    [t('First token latency'), metricText(bar.firstTokenLatencyMS, t)],
    [t('Endpoint latency'), metricText(bar.endpointLatencyMS, t)],
    [t('Conversation latency'), metricText(bar.latencyMS, t)],
    [`${t('Input tokens')} / ${t('Output tokens')}`, tokenText(bar.promptTokens, bar.completionTokens)],
  ]
  return (
    <div className='grid min-w-56 gap-1 text-left'>
      {rows.map(([label, value]) => (
        <div key={label} className='grid grid-cols-[auto_1fr] gap-3'>
          <span className='opacity-70'>{label}</span>
          <span className='text-right font-medium'>{value}</span>
        </div>
      ))}
      {bar.message && <div className='mt-1 max-w-64 break-words opacity-80'>{bar.message}</div>}
    </div>
  )
}
```

Wrap history bars:

```tsx
<TooltipProvider delay={120}>
  <div className='border-border/50 bg-background/45 flex h-10 items-center gap-1 rounded-lg border px-2 py-2'>
    {bars.map((bar) => (
      <Tooltip key={bar.id}>
        <TooltipTrigger
          className={cn(
            'h-full min-w-0 flex-1 rounded-full transition-opacity hover:opacity-80 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none',
            monitorHistoryToneClass(bar.tone),
            bar.tone === 'empty' && 'opacity-25'
          )}
          aria-label={historyAriaLabel(bar, t)}
        />
        <TooltipContent className='max-w-sm'>
          <MonitorHistoryDetails bar={bar} />
        </TooltipContent>
      </Tooltip>
    ))}
  </div>
</TooltipProvider>
```

Add `historyAriaLabel`:

```ts
function historyAriaLabel(bar: MonitorHistoryBar, t: TFn) {
  if (bar.status === 'empty') return t('No data')
  return `${monitorStatusText(bar.status, t)} · ${bar.model || t('No data')}`
}
```

- [ ] **Step 4: Add summary context**

Use `info.latest_model` before record fallback:

```ts
const latestModel =
  info?.latest_model?.trim() ||
  latestRecord?.model?.trim() ||
  channel?.test_model ||
  ''
```

Add secondary metrics:

```tsx
<SecondaryMetric
  icon={Clock}
  label={t('Average first token latency')}
  value={metricText(info?.average_first_token_latency_ms, t)}
/>
<SecondaryMetric
  icon={CheckCircle2}
  label={t('Successful checks')}
  value={`${info?.seven_day_successes ?? 0} / ${info?.seven_day_checks ?? 0}`}
/>
```

Import `CheckCircle2` from `lucide-react`.

- [ ] **Step 5: Run focused dialog test and verify pass**

Run from `web/default`:

```powershell
bun test src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
git commit -m "feat: improve channel monitor history details"
```

---

### Task 4: i18n And Visual Verification

**Files:**
- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`
- Modify other locale JSON files only when the sync command updates them.

- [ ] **Step 1: Find missing frontend i18n keys**

Run from `web/default`:

```powershell
bun run i18n:sync
```

Expected: Translation files are updated or the command reports no missing keys.

- [ ] **Step 2: Ensure important Chinese labels are present**

Verify `web/default/src/i18n/locales/zh.json` contains translations for:

```json
{
  "Average first token latency": "平均首 token 延迟",
  "Successful checks": "成功检测",
  "Endpoint latency": "端点延迟",
  "Time": "时间",
  "Model": "模型",
  "Status": "状态"
}
```

If `i18n:sync` adds empty or English fallback values, replace only these keys with the Chinese values above.

- [ ] **Step 3: Run frontend focused tests**

Run from `web/default`:

```powershell
bun test src/features/channels/lib/channel-monitor.test.ts src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
```

Expected: PASS.

- [ ] **Step 4: Run frontend type/build check**

Run from `web/default`:

```powershell
bun run build
```

Expected: PASS.

- [ ] **Step 5: Check the monitor dialog in browser**

Use the running local app at `http://localhost:3001/channels`. Open a channel monitor dialog and verify:

- light theme uses light monitor surfaces;
- dark theme uses dark monitor surfaces;
- history bar tooltip shows model, status, time, latency, token counts, and error text;
- no-data channels show no-data labels instead of `0 ms`;
- the dialog stays within the viewport.

- [ ] **Step 6: Commit**

Run:

```powershell
git add web/default/src/i18n/locales web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
git commit -m "i18n: translate channel monitor metrics"
```

If the i18n command produces no file changes, skip the commit and record that no i18n commit was needed.

---

### Task 5: Review And Full Verification

**Files:**
- No planned source changes unless review finds a real issue.

- [ ] **Step 1: Run backend verification**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend verification**

Run from `web/default`:

```powershell
bun test src/features/channels/lib/channel-monitor.test.ts src/features/channels/components/dialogs/channel-monitor-dialog.test.ts
bun run build
```

Expected: PASS.

- [ ] **Step 3: Ask Claude for focused review**

Create `.codex/claude-channel-monitor-metrics-review-prompt.md` with:

```markdown
Review the current diff for Wynth API channel monitor metrics display.

Focus on:
- backend monitor metric contract correctness;
- SQLite/MySQL/PostgreSQL compatibility;
- first-token latency and zero-value semantics;
- frontend theme consistency;
- i18n completeness;
- whether the implementation stays scoped to single-channel monitor dialog improvements.

Return findings only. If there are no blocking issues, say APPROVED.
```

Run:

```powershell
Get-Content -Raw .codex\claude-channel-monitor-metrics-review-prompt.md | claude -p --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

If default Claude settings fail due to quota, retry with:

```powershell
Get-Content -Raw .codex\claude-channel-monitor-metrics-review-prompt.md | claude -p --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

- [ ] **Step 4: Apply only verified review fixes**

For each Claude finding, inspect the cited files and only patch issues that are real defects or clear requirement misses. Do not broaden scope into a global monitor dashboard or priority scoring.

- [ ] **Step 5: Re-run verification after fixes**

Run the same backend and frontend commands from Steps 1 and 2.

Expected: PASS.

- [ ] **Step 6: Commit review fixes if any**

Run:

```powershell
git add model/channel_monitor.go model/channel_monitor_test.go controller/channel_monitor_test.go web/default/src/features/channels/types.ts web/default/src/features/channels/lib/channel-monitor.ts web/default/src/features/channels/lib/channel-monitor.test.ts web/default/src/features/channels/components/dialogs/channel-monitor-dialog.tsx web/default/src/features/channels/components/dialogs/channel-monitor-dialog.test.ts web/default/src/i18n/locales
git commit -m "fix: address channel monitor metrics review"
```

Skip this commit if no review fixes are needed.
