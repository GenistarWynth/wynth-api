Review the upstream source rule-driven sync backend changes on branch upstream-source-sync.

Focus on:
- rule matcher correctness;
- unmatched groups staying visible but unsynced;
- fixed model intersection behavior;
- explicit false monitor/auto-sync override preservation;
- generated channel ownership safety;
- SQLite/MySQL/PostgreSQL compatibility;
- project JSON wrapper usage.

Return only findings with file/line references and severity.
