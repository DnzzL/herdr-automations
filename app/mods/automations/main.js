// Automations: scheduled agent prompts (herdr-automations), as a side panel.
//
// Everything goes through the herdr-automations CLI (`… --json`): this mod
// never reads or writes automations.yaml or history.jsonl itself. The files
// are watched only to know when to ask again.

import { View, div, svg } from "gpui-kit";
import { v_flex, h_flex, InputState, Input, TextareaState, Textarea } from "gpui-base";
import { exec, hc, home, theme, terminals, spaces, run, notify, pickFolder, watchFolder, knownAgents } from "herdr";

const PRESETS = [
  ["Hourly", "0 * * * *"],
  ["Every morning", "0 9 * * *"],
  ["Weekdays 9:00", "0 9 * * 1-5"],
  ["Mondays 9:00", "0 9 * * 1"],
  ["Fridays 17:00", "0 17 * * 5"],
];
// The agents hc can hand a prompt to and watch to the end (config.KindTakesPromptArg).
// The form offers the ones installed here.
const AGENTS = ["claude", "codex", "pi", "copilot"];
const WORKSPACES = [["worktree", "New worktree"], ["root", "Repo root"], ["existing", "Existing space"]];
const FINISHED = new Set(["done", "failed", "cancelled"]);

const human = (cron) => PRESETS.find(([, c]) => c === cron)?.[0] ?? cron;
const tilde = (p) => (p && p.startsWith(home()) ? "~" + p.slice(home().length) : p ?? "");
function ago(iso) {
  const s = (Date.now() - Date.parse(iso)) / 1000;
  if (s < 60) return "now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}
function until(iso) {
  const s = (Date.parse(iso) - Date.now()) / 1000;
  if (s < 3600) return `in ${Math.max(1, Math.round(s / 60))}m`;
  const d = new Date(iso), hm = d.toTimeString().slice(0, 5);
  if (s < 86400 && d.getDate() === new Date().getDate()) return `today ${hm}`;
  // Spelled out: the mod runtime ignores toLocaleDateString's options.
  return `${["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][d.getDay()]} ${hm}`;
}
function duration(sec) {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  return `${Math.floor(sec / 3600)}h${String(Math.round((sec % 3600) / 60)).padStart(2, "0")}`;
}

export default class Automations extends View {
  init(_props, cx) {
    this.list = null; // `ls --json`
    this.runs = []; // `history --json`
    this.view = { kind: "list" }; // list | detail {name} | form {original}
    this.error = null; this.note = null;
    this.known = null; // run ids already seen by this copy, to toast only new failures
    this.handle = cx.focus_handle();
    cx.spawn(async (cx) => {
      this.bin = await this.locate();
      await this.refresh(cx);
      this.watch(cx);
    });
    cx.timer.every(30000, (cx) => this.refresh(cx)); // next-run times and the scheduler's pid
  }

  // The CLI on PATH, else where install.sh and the dev symlink put it.
  async locate() {
    try { if ((await exec("herdr-automations", ["version"])).code === 0) return "herdr-automations"; } catch {}
    return `${home()}/.local/bin/herdr-automations`;
  }

  async cli(args) {
    const r = await exec(this.bin, args);
    if (r.code !== 0) throw new Error((r.stderr || r.stdout).trim().replace(/^error: /, ""));
    return r.stdout;
  }

  async refresh(cx) {
    if (!this.bin) return;
    try {
      this.list = JSON.parse(await this.cli(["ls", "--json"]));
      this.runs = JSON.parse(await this.cli(["history", "--json"]));
      this.error = null;
      this.announce();
    } catch (e) { this.error = String(e.message ?? e); }
    cx.notify();
  }

  // Config and state folders: a change there is a change to show.
  watch(cx) {
    const dirs = [this.list?.config?.replace(/\/[^/]+$/, ""), this.list?.state].filter(Boolean);
    for (const dir of dirs) cx.spawn(async (cx) => {
      for (;;) { try { await watchFolder(dir); } catch { return; } await this.refresh(cx); }
    });
  }

  // A failure finds you (ADR 0007): toast runs that failed or were missed
  // since this copy started. The first read only learns what is there.
  announce() {
    const ids = new Set(this.runs.map((r) => r.run_id));
    if (this.known) for (const r of this.runs) {
      if (this.known.has(r.run_id) || !(r.status === "failed" || r.status === "missed")) continue;
      notify({
        title: `${r.automation} ${r.status === "failed" ? "failed" : "missed a run"}`,
        body: r.error ?? "", level: r.status === "failed" ? "error" : "warning",
        ...(r.terminal && this.live(r.terminal) ? { action: { name: "pane.focus", args: { terminal: r.terminal } } } : {}),
      });
    }
    this.known = ids;
  }

  live(address) { return terminals().find((t) => t.address === address && t.state === "live"); }
  liveFor(name) { return terminals().find((t) => t.labels?.automation === name && t.state === "live"); }

  act(cx, args, then) {
    this.note = null;
    cx.spawn(async (cx) => {
      try { await this.cli(args); then?.(); } catch (e) { this.note = String(e.message ?? e); }
      await this.refresh(cx);
    });
  }

  open(run_, cx) {
    if (!run_.seen && FINISHED.has(run_.status)) this.act(cx, ["seen", run_.run_id]);
    if (run_.terminal && this.live(run_.terminal)) run("pane.focus", { terminal: run_.terminal });
    else { this.view = { kind: "detail", name: run_.automation }; cx.notify(); }
  }

  // ---- form ----

  edit(original, cx) {
    const a = this.list?.automations.find((x) => x.name === original) ?? {
      name: "", cron: "0 9 * * 1-5", repo: "", workspace: "worktree", agent: "claude", prompt: "", timeout_minutes: 60,
    };
    const field = (value, placeholder) => {
      const s = InputState.new({ placeholder, value: value ?? "" });
      s.on("change", (_e, cx) => this.preview(cx));
      return s;
    };
    this.form = {
      original, workspace: a.workspace ?? "worktree", workspace_id: a.workspace_id ?? "",
      agent: a.template ? "" : a.agent ?? "claude", template: a.template ?? "",
      name: field(a.name, "issue-triage"), cron: field(a.cron, "0 9 * * 1-5"), repo: field(tilde(a.repo), "~/Projects/myapp"),
      model: field(a.model, "agent default"), timeout: field(String(a.timeout_minutes ?? 60), "60"),
      mcp: field(tilde(a.mcp_config), "optional: path to an MCP servers JSON"),
      prompt: TextareaState.new({ placeholder: "What should the agent do each time?", value: a.prompt ?? "" }),
      result: null, seq: 0,
    };
    this.form.prompt.set_auto_grow(4, 12);
    this.form.prompt.on("change", (_e, cx) => this.preview(cx));
    this.view = { kind: "form" };
    cx.spawn(async (cx) => {
      try { this.templates = JSON.parse((await hc(["templates", "--json"])).stdout).templates ?? []; } catch { this.templates = []; }
      try { this.installed = (await knownAgents()).filter((a) => a.installed).map((a) => a.key); } catch { this.installed = null; }
      cx.notify();
    });
    this.preview(cx);
  }

  entry() {
    const f = this.form, num = parseInt(f.timeout.value(), 10);
    const expand = (p) => (p.startsWith("~/") ? home() + p.slice(1) : p);
    return {
      name: f.name.value().trim(), cron: f.cron.value().trim(), repo: expand(f.repo.value().trim()),
      workspace: f.workspace, workspace_id: f.workspace === "existing" ? f.workspace_id : "",
      agent: f.template ? "" : f.agent, template: f.template, model: f.template ? "" : f.model.value().trim(),
      prompt: f.prompt.value(), mcp_config: expand(f.mcp.value().trim()), timeout_minutes: Number.isFinite(num) ? num : 0,
    };
  }

  async put(dryRun) {
    const f = this.form, args = ["put", ...(f.original ? ["--original", f.original] : []), ...(dryRun ? ["--dry-run"] : [])];
    // exec has no stdin: hand the JSON over through the shell.
    const r = await exec("sh", ["-c", 'bin=$1 json=$2; shift 2; printf %s "$json" | "$bin" "$@"', "sh", this.bin, JSON.stringify(this.entry()), ...args]);
    if (r.code !== 0) throw new Error((r.stderr || r.stdout).trim());
    return JSON.parse(r.stdout);
  }

  preview(cx) {
    const f = this.form, seq = ++f.seq;
    cx.spawn(async (cx) => {
      let result;
      try { result = await this.put(true); } catch (e) { result = { problem: String(e.message ?? e) }; }
      if (this.form === f && seq === f.seq) { f.result = result; cx.notify(); }
    });
  }

  save(cx) {
    const f = this.form;
    cx.spawn(async (cx) => {
      try {
        const res = await this.put(false);
        if (res.problem) f.result = res;
        else { this.view = { kind: "detail", name: this.entry().name }; this.form = null; }
      } catch (e) { f.result = { problem: String(e.message ?? e) }; }
      await this.refresh(cx);
    });
  }

  // ---- render ----

  render(cx) {
    const t = theme();
    const body = this.view.kind === "form" && this.form ? this.renderForm(t, cx)
      : this.view.kind === "detail" ? this.renderDetail(t, cx) : this.renderList(t, cx);
    return v_flex().id("automations").size_full().min_h(0).rounded(10).overflow_hidden().bg(t.background)
      .border_1().border_color(t.border).text_color(t.foreground).text_size(13).track_focus(this.handle)
      .child(this.header(t, cx))
      .when(Boolean(this.list && !this.list.scheduler), (el) => el.child(this.banner(t, cx)))
      .when(Boolean(this.error || this.note), (el) => el.child(div().px(14).py(6).text_size(12).text_color(t.ansi1).child(this.error || this.note)))
      .child(div().id("automations-body").flex_1().min_h(0).overflow_y_scrollbar().child(body));
  }

  button(id, label, t, onClick, opts = {}) {
    return h_flex().id(id).h(opts.h ?? 26).px(opts.px ?? 9).flex_none().items_center().justify_center().rounded(7).text_size(12)
      .bg(opts.solid ? t.solid : t.raised).text_color(opts.solid ? t.solid_foreground : opts.danger ? t.ansi1 : t.foreground)
      .border_1().border_color(opts.solid ? t.solid : t.border).cursor_pointer().hover((s) => s.opacity(0.85))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => onClick(cx)).child(label);
  }

  pills(id, options, value, t, pick) {
    return h_flex().flex_wrap().gap(5).children(options.map(([v, label]) =>
      h_flex().id(`${id}-${v}`).h(24).px(8).items_center().rounded(6).text_size(12).cursor_pointer()
        .bg(v === value ? t.selected : t.raised).text_color(v === value ? t.foreground : t.muted)
        .border_1().border_color(v === value ? t.accent : t.border).hover((s) => s.bg(t.hover))
        .on_click((_e, cx) => pick(v, cx)).child(label)));
  }

  switch_(id, on, t, toggle) {
    return h_flex().id(id).w(30).h(17).flex_none().rounded(9).p(2).cursor_pointer().bg(on ? t.accent : t.input)
      .when(on, (el) => el.justify_end()).on_click((_e, cx) => toggle(cx))
      .child(div().size(13).rounded(7).bg(t.background));
  }

  header(t, cx) {
    const paused = Boolean(this.list?.paused);
    const back = this.view.kind !== "list";
    return h_flex().h(42).flex_none().px(14).gap(8).items_center().border_b(1).border_color(t.border)
      .when(back, (el) => el.child(this.button("back", "‹", t, (cx) => { this.view = { kind: "list" }; this.form = null; cx.notify(); }, { px: 7 })))
      .child(div().font_semibold().child("Automations"))
      .child(div().flex_1())
      .when(Boolean(this.list), (el) => el
        .child(div().text_size(11).text_color(paused ? t.blocked : t.dim).child(paused ? "All paused" : "Active"))
        .child(this.switch_("pause-all", !paused, t, (cx) => this.act(cx, [paused ? "resume" : "pause", "--all"]))))
      .when(!back, (el) => el.child(this.button("new", "+", t, (cx) => this.edit(null, cx), { px: 8 })));
  }

  banner(t, cx) {
    return h_flex().px(14).py(8).gap(8).items_center().bg(t.blocked + "22").border_b(1).border_color(t.border)
      .child(div().flex_1().text_size(12).child("Scheduler stopped: nothing will run on time."))
      .child(this.button("start", "Start", t, (cx) => this.act(cx, ["service", "install"]), { solid: true }));
  }

  dot(a, t) {
    const live = this.liveFor(a.name);
    const color = live ? (live.status === "blocked" ? t.blocked : t.working)
      : a.disabled || this.list.paused ? t.dim : a.last?.status === "failed" || a.last?.status === "missed" ? t.ansi1 : t.idle;
    return div().size(8).rounded(4).flex_none().bg(color);
  }

  renderList(t, cx) {
    if (!this.list) return div().p(14).text_color(t.dim).child(this.error ? "" : "Loading…");
    const inbox = this.runs.filter((r) => FINISHED.has(r.status) && !r.seen).slice(0, 20);
    const section = (label, n) => h_flex().px(14).pt(12).pb(4).gap(6).items_center().text_size(11).font_semibold().text_color(t.dim)
      .child(label).when(n > 0, (el) => el.child(div().px(5).rounded(4).bg(t.raised).child(String(n))));
    const col = v_flex().pb(10);
    col.child(section("INBOX", inbox.length).when(inbox.length > 0, (el) => el.child(div().flex_1())
      .child(div().id("seen-all").font_normal().cursor_pointer().hover((s) => s.text_color(t.foreground))
        .on_click((_e, cx) => this.act(cx, ["seen", "--all"])).child("Mark all read"))));
    if (!inbox.length) col.child(div().px(14).py(4).text_size(12).text_color(t.dim).child("Nothing new."));
    for (const r of inbox) col.child(this.inboxRow(r, t));
    col.child(section("AUTOMATIONS", 0));
    if (!this.list.automations.length && !this.list.invalid.length)
      col.child(div().px(14).py(4).text_size(12).text_color(t.dim).child("None yet. + creates one."));
    for (const a of this.list.automations) col.child(this.row(a, t));
    for (const d of this.list.invalid) col.child(v_flex().mx(8).my(2).px(8).py(6).rounded(7).bg(t.ansi1 + "18")
      .child(div().text_size(12).font_semibold().child(d.name || `line ${d.line}`))
      .child(div().text_size(11).text_color(t.muted).child(d.message)));
    return col;
  }

  inboxRow(r, t) {
    const ok = r.status === "done", color = ok ? t.ansi2 : r.status === "failed" ? t.ansi1 : t.dim;
    return v_flex().id(`inbox-${r.run_id}`).mx(8).px(8).py(6).gap(2).rounded(7).cursor_pointer().hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => this.open(r, cx))
      .child(h_flex().gap(6).items_center()
        .child(div().size(7).rounded(4).flex_none().bg(color))
        .child(div().flex_1().min_w(0).truncate().child(r.automation))
        .child(div().text_size(11).text_color(color).child(r.status))
        .child(div().text_size(11).text_color(t.dim).child(ago(r.at))))
      .child(div().pl(13).text_size(11).text_color(t.muted).truncate()
        .child(r.error || `${duration(r.seconds)}${r.terminal && this.live(r.terminal) ? " · open" : ""}`));
  }

  row(a, t) {
    const live = this.liveFor(a.name);
    const sub = live ? `running · ${live.status ?? "starting"}` : a.disabled ? "paused"
      : `${human(a.cron)} · ${this.list.paused ? "held" : until(a.next)}`;
    return v_flex().id(`auto-${a.name}`).mx(8).px(8).py(7).gap(2).rounded(7).cursor_pointer().hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => { this.view = { kind: "detail", name: a.name }; cx.notify(); })
      .child(h_flex().gap(7).items_center().child(this.dot(a, t)).child(div().flex_1().min_w(0).truncate().font_semibold().child(a.name))
        .child(this.button(`run-${a.name}`, "▶", t, (cx) => this.act(cx, ["run", a.name, "--bg"]), { px: 6, h: 22 })))
      .child(div().pl(15).text_size(11).text_color(t.muted).truncate().child(sub))
      .child(div().pl(15).text_size(11).text_color(t.dim).truncate().child(`${a.template ? "template " + a.template : a.agent + (a.model ? " · " + a.model : "")} · ${a.workspace}`));
  }

  renderDetail(t, cx) {
    const a = this.list?.automations.find((x) => x.name === this.view.name);
    if (!a) return div().p(14).text_color(t.dim).child("This automation is gone.");
    const runs = this.runs.filter((r) => r.automation === a.name && r.status !== "scheduled").slice(0, 30);
    const kv = (k, v) => h_flex().gap(8).text_size(12).child(div().w(78).flex_none().text_color(t.dim).child(k)).child(div().flex_1().min_w(0).truncate().child(v || "—"));
    const deleting = this.deleting === a.name;
    return v_flex().p(14).gap(10)
      .child(h_flex().gap(8).items_center().child(this.dot(a, t)).child(div().flex_1().min_w(0).truncate().text_size(15).font_semibold().child(a.name)))
      .child(h_flex().gap(6).flex_wrap()
        .child(this.button("d-run", "Run now", t, (cx) => this.act(cx, ["run", a.name, "--bg"]), { solid: true }))
        .child(this.button("d-pause", a.disabled ? "Resume" : "Pause", t, (cx) => this.act(cx, [a.disabled ? "resume" : "pause", a.name])))
        .child(this.button("d-edit", "Edit", t, (cx) => this.edit(a.name, cx)))
        .child(this.button("d-rm", deleting ? "Confirm delete" : "Delete", t, (cx) => {
          if (!deleting) { this.deleting = a.name; cx.notify(); return; }
          this.deleting = null;
          this.act(cx, ["rm", a.name], () => { this.view = { kind: "list" }; });
        }, { danger: true })))
      .child(v_flex().gap(4)
        .child(kv("Schedule", `${human(a.cron)}${human(a.cron) !== a.cron ? ` (${a.cron})` : ""}`))
        .child(kv("Next", a.disabled ? "paused" : this.list.paused ? "held: all paused" : until(a.next)))
        .child(kv("Repo", tilde(a.repo)))
        .child(kv("Runs in", WORKSPACES.find(([w]) => w === a.workspace)?.[1] ?? a.workspace))
        .child(kv(a.template ? "Template" : "Agent", a.template || `${a.agent}${a.model ? " · " + a.model : ""}`)))
      .child(div().p(8).rounded(7).bg(t.raised).text_size(12).text_color(t.muted).child(a.prompt || a.workflow || ""))
      .child(div().pt(4).text_size(11).font_semibold().text_color(t.dim).child("HISTORY"))
      .children(runs.length ? runs.map((r) => h_flex().id(`h-${r.run_id}`).gap(8).px(6).py(4).rounded(6).items_center().cursor_pointer()
        .hover((s) => s.bg(t.hover)).on_click((_e, cx) => this.open(r, cx))
        .child(div().w(66).flex_none().text_size(11).text_color(r.status === "done" ? t.ansi2 : r.status === "failed" || r.status === "missed" ? t.ansi1 : t.muted).child(r.status))
        .child(div().flex_1().min_w(0).truncate().text_size(11).text_color(t.muted).child(r.error || (r.seconds ? duration(r.seconds) : "")))
        .child(div().flex_none().text_size(11).text_color(t.dim).child(ago(r.at))))
        : [div().text_size(12).text_color(t.dim).child("No runs yet.")]);
  }

  renderForm(t, cx) {
    const f = this.form;
    const label = (s) => div().pt(6).text_size(11).font_semibold().text_color(t.dim).child(s);
    const input = (id, state) => div().id(id).h(30).px(9).flex().items_center().rounded(7).border_1().border_color(t.input).bg(t.surface ?? t.background)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).child(Input.new(state).h_full().w_full().text_size(12));
    const cron = f.cron.value().trim();
    const presets = [...PRESETS.map(([l, c]) => [c, l]), ["custom", "Custom"]];
    const preset = PRESETS.some(([, c]) => c === cron) ? cron : "custom";
    // Installed ones, and the entry's own agent even if it is not (any more).
    const offered = AGENTS.filter((a) => !this.installed || this.installed.includes(a) || a === f.agent);
    const agents = [...offered.map((a) => [a, a]), ...(this.templates ?? []).map((tp) => [`template:${tp.name}`, `${tp.name} (template)`])];
    const agentValue = f.template ? `template:${f.template}` : f.agent;
    const res = f.result;
    return v_flex().p(14).gap(4)
      .child(div().text_size(15).font_semibold().child(f.original ? `Edit ${f.original}` : "New automation"))
      .child(label("NAME")).child(input("f-name", f.name))
      .child(label("SCHEDULE")).child(this.pills("f-preset", presets, preset, t, (v, cx) => { if (v !== "custom") f.cron.set_value(v); this.preview(cx); cx.notify(); }))
      .child(input("f-cron", f.cron))
      .child(label("REPO")).child(h_flex().gap(6).child(div().flex_1().child(input("f-repo", f.repo)))
        .child(this.button("f-pick", "Choose…", t, (cx) => cx.spawn(async (cx) => {
          const dir = await pickFolder({ prompt: "Repository for this automation" });
          if (dir) { f.repo.set_value(tilde(dir)); this.preview(cx); }
          cx.notify();
        }), { h: 30 })))
      .child(label("RUNS IN")).child(this.pills("f-ws", WORKSPACES, f.workspace, t, (v, cx) => { f.workspace = v; this.preview(cx); cx.notify(); }))
      .when(f.workspace === "existing", (el) => el.child(this.pills("f-space",
        spaces().filter((s) => !s.machine).map((s) => [s.id, s.name]), f.workspace_id, t,
        (v, cx) => { f.workspace_id = v; this.preview(cx); cx.notify(); })))
      .child(label("AGENT")).child(this.pills("f-agent", agents, agentValue, t, (v, cx) => {
        if (v.startsWith("template:")) { f.template = v.slice(9); f.agent = ""; } else { f.template = ""; f.agent = v; }
        this.preview(cx); cx.notify();
      }))
      .when(!f.template, (el) => el.child(label("MODEL")).child(input("f-model", f.model)))
      .child(label("PROMPT")).child(div().id("f-prompt").p(8).rounded(7).border_1().border_color(t.input)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).child(Textarea.new(f.prompt).w_full().text_size(12)))
      .child(label("TIMEOUT (MINUTES)")).child(input("f-timeout", f.timeout))
      .child(label("MCP CONFIG")).child(input("f-mcp", f.mcp))
      .child(div().pt(8).text_size(12)
        .when(Boolean(res?.problem), (el) => el.text_color(t.ansi1).child(res.problem))
        .when(Boolean(res && !res.problem && res.next), (el) => el.text_color(t.muted)
          .child(`Next: ${res.next.map((n) => until(n)).join(", ")}`)))
      .child(h_flex().pt(8).gap(8).justify_end()
        .child(this.button("f-cancel", "Cancel", t, (cx) => { this.view = f.original ? { kind: "detail", name: f.original } : { kind: "list" }; this.form = null; cx.notify(); }))
        .child(this.button("f-save", "Save", t, (cx) => this.save(cx), { solid: true })));
  }
}
