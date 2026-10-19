// Small single-page app, no framework. Routes live in the URL hash:
// #/challenges, #/scoreboard, #/login, #/admin.
"use strict";

const $ = (sel) => document.querySelector(sel);
const view = $("#view");

// ---------- session (JWT kept in localStorage) ----------
function loadSession() {
  try { return JSON.parse(localStorage.getItem("ctf.session")); } catch { return null; }
}
let session = loadSession(); // { token, user: {id, username, role} }

function setSession(s) {
  session = s;
  try {
    if (s) localStorage.setItem("ctf.session", JSON.stringify(s));
    else localStorage.removeItem("ctf.session");
  } catch { /* storage blocked: session lasts until reload */ }
  renderHeader();
}

// ---------- helpers ----------
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

// One message line under the header, for success and error notices.
function message(text, isError = false) {
  const m = $("#msg");
  m.textContent = text;
  m.className = isError ? "err" : "ok";
  m.hidden = !text;
}

// Change page and show a message there. (Setting location.hash directly and
// then calling message() would lose the message: the hashchange handler
// clears it.)
let pendingMsg = null;
function go(hash, text = "", isError = false) {
  if (location.hash === hash) { route(); message(text, isError); return; }
  pendingMsg = [text, isError];
  location.hash = hash;
}

// Every API call goes through here. It attaches the token and parses JSON.
async function api(path, { method = "GET", body } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (session?.token) headers["Authorization"] = "Bearer " + session.token;

  let res;
  try {
    res = await fetch(path, { method, headers, body: body !== undefined ? JSON.stringify(body) : undefined });
  } catch {
    return { ok: false, status: 0, data: { error: "network error" } };
  }

  let data = null;
  if (res.status !== 204) {
    try { data = await res.json(); } catch { data = null; }
  }

  // Expired/invalid token: drop the session and go to the login page.
  if (res.status === 401 && session && !path.startsWith("/api/auth/login")) {
    setSession(null);
    go("#/login", "Your session expired, please log in again.", true);
  }
  return { ok: res.ok, status: res.status, data, headers: res.headers };
}

function errorText(r) {
  if (r.status === 501) return "Not implemented yet (" + (r.data?.error || "TODO") + ")";
  if (r.status === 502 || r.status === 503) return "Service unavailable, try again.";
  return r.data?.error || `Request failed (${r.status})`;
}

// ---------- header ----------
function renderHeader() {
  const s = $("#session");
  if (session) {
    s.innerHTML = `Logged in as <b>${esc(session.user.username)}</b>${session.user.role === "admin" ? " (admin)" : ""}
      <button id="logout">Log out</button>`;
    $("#logout").onclick = () => {
      // JWTs are stateless: logging out = forgetting the token.
      setSession(null);
      go("#/login", "Logged out.");
    };
  } else {
    s.innerHTML = `<a href="#/login">Log in</a>`;
  }
  document.querySelectorAll(".admin-only").forEach((el) => {
    el.hidden = session?.user.role !== "admin";
  });
}

function highlightNav(route) {
  document.querySelectorAll("#nav a").forEach((a) =>
    a.classList.toggle("active", a.dataset.route === route));
}

// ---------- login / register ----------
function pageLogin(mode = "login") {
  const other = mode === "login" ? "register" : "login";
  view.innerHTML = `
    <h1>${mode === "login" ? "Log in" : "Register"}</h1>
    <form id="authform">
      <p><label for="u">Username</label>
        <input id="u" name="username" autocomplete="username" required minlength="3" maxlength="32"></p>
      <p><label for="p">Password</label>
        <input id="p" name="password" type="password" required
               autocomplete="${mode === "login" ? "current-password" : "new-password"}"
               ${mode === "register" ? 'minlength="8" maxlength="72"' : ""}></p>
      <p><button type="submit">${mode === "login" ? "Log in" : "Register"}</button></p>
    </form>
    <p class="muted">${mode === "login" ? "No account yet?" : "Already registered?"}
      <a href="#" id="switch">${other === "login" ? "Log in" : "Register"}</a></p>`;

  $("#switch").onclick = (e) => { e.preventDefault(); message(""); pageLogin(other); };
  $("#authform").onsubmit = async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    const r = await api(`/api/auth/${mode}`, {
      method: "POST",
      body: { username: f.get("username"), password: f.get("password") },
    });
    if (!r.ok) return message(errorText(r), true);
    setSession({ token: r.data.token, user: r.data.user });
    go("#/challenges", `Welcome, ${r.data.user.username}.`);
  };
}

// ---------- challenges ----------
async function pageChallenges() {
  if (!session) { location.hash = "#/login"; return; }
  view.innerHTML = `<h1>Challenges</h1><div id="list">Loading...</div><div id="detail"></div>`;

  const [ch, mine] = await Promise.all([api("/api/challenges"), api("/api/submissions/me")]);
  if (!ch.ok) { $("#list").textContent = errorText(ch); return; }

  // The solved list is optional: if submissions isn't implemented yet, carry on.
  const solved = new Set(mine.ok && Array.isArray(mine.data) ? mine.data.map((s) => s.challenge_id) : []);
  const list = (Array.isArray(ch.data) ? ch.data : [])
    .sort((a, b) => a.category.localeCompare(b.category) || a.points - b.points);

  if (!list.length) {
    $("#list").innerHTML = `<p>No challenges yet.${session.user.role === "admin" ? ' Add some on the <a href="#/admin">admin</a> page.' : ""}</p>`;
    return;
  }

  $("#list").innerHTML = `
    <table>
      <tr><th>Category</th><th>Challenge</th><th class="num">Points</th><th>Status</th></tr>
      ${list.map((c) => `
        <tr>
          <td>${esc(c.category)}</td>
          <td><a href="#" data-id="${c.id}">${esc(c.title)}</a></td>
          <td class="num">${c.points}</td>
          <td>${solved.has(c.id) ? '<span class="solved">solved</span>' : ""}</td>
        </tr>`).join("")}
    </table>`;

  document.querySelectorAll("#list a[data-id]").forEach((a) => {
    a.onclick = (e) => {
      e.preventDefault();
      showChallenge(list.find((c) => c.id === Number(a.dataset.id)), solved.has(Number(a.dataset.id)));
    };
  });
}

function showChallenge(c, isSolved) {
  $("#detail").innerHTML = `
    <div class="box">
      <h2>${esc(c.title)}</h2>
      <p class="muted">${esc(c.category)}, ${c.points} points${isSolved ? ", already solved" : ""}</p>
      <pre class="desc">${esc(c.description || "No description.")}</pre>
      <form id="flagform">
        <p><label for="flag">Flag</label>
          <input id="flag" name="flag" placeholder="flag{...}" autocomplete="off" required></p>
        <p><button type="submit">Submit</button></p>
      </form>
    </div>`;
  $("#flag").focus();
  $("#flagform").onsubmit = async (e) => {
    e.preventDefault();
    const r = await api("/api/submissions", {
      method: "POST",
      body: { challenge_id: c.id, flag: new FormData(e.target).get("flag") },
    });
    if (!r.ok) return message(errorText(r), true);
    if (!r.data.correct) return message("Wrong flag.", true);
    message(r.data.already_solved
      ? "Correct, but you had already solved this one."
      : `Correct! +${r.data.points} points.`);
    pageChallenges();
  };
}

// ---------- scoreboard ----------
let scoreTimer = null;
async function pageScoreboard() {
  view.innerHTML = `<h1>Scoreboard</h1><p class="muted">Refreshes every 10 seconds.</p><div id="board">Loading...</div>`;
  const load = async () => {
    const r = await api("/api/scoreboard?limit=50");
    const board = $("#board");
    if (!board) return;
    if (!r.ok) { board.textContent = errorText(r); return; }
    const rows = Array.isArray(r.data) ? r.data : [];
    if (!rows.length) { board.textContent = "No points scored yet."; return; }
    board.innerHTML = `
      <table>
        <tr><th class="num">Rank</th><th>Player</th><th class="num">Score</th></tr>
        ${rows.map((e) => `
          <tr class="${session?.user.username === e.username ? "me" : ""}">
            <td class="num">${e.rank}</td><td>${esc(e.username)}</td><td class="num">${e.score}</td>
          </tr>`).join("")}
      </table>`;
  };
  await load();
  scoreTimer = setInterval(load, 10000);
}

// ---------- admin ----------
async function pageAdmin() {
  if (session?.user.role !== "admin") { location.hash = "#/challenges"; return; }
  view.innerHTML = `
    <h1>Admin</h1>

    <h2>System status</h2>
    <p class="muted">Which pods are answering, and which version they run. Only admins can see this.
      "Send 20 requests" shows the load balancer spreading requests over the pods.</p>
    <p><button id="st-refresh">Refresh</button> <button id="st-burst">Send 20 requests</button></p>
    <div id="sysstatus">Loading...</div>

    <h2>Challenges</h2>
    <div id="adminlist">Loading...</div>
    <h2 id="formtitle">New challenge</h2>
    <form id="chform">
      <input type="hidden" name="cid">
      <p><label>Title</label><input name="title" required maxlength="100"></p>
      <p><label>Category</label><input name="category" required placeholder="web, pwn, crypto..." maxlength="30"></p>
      <p><label>Points</label><input name="points" type="number" min="1" max="1000" value="100" required></p>
      <p><label>Description</label><textarea name="description"></textarea></p>
      <p><label>Flag <span id="flaghint" class="muted"></span></label><input name="flag" placeholder="flag{...}" autocomplete="off"></p>
      <p><button type="submit">Save</button> <button type="button" id="resetform">Clear</button></p>
    </form>`;

  const form = $("#chform");
  // Read fields via form.elements: form.id / form.title would return the
  // <form>'s own attributes, not the inputs with those names.
  const f = (name) => form.elements.namedItem(name);
  const resetForm = () => {
    form.reset();
    f("cid").value = "";
    $("#formtitle").textContent = "New challenge";
    $("#flaghint").textContent = "";
    f("flag").required = true;
  };
  resetForm();
  $("#resetform").onclick = resetForm;

  const loadList = async () => {
    const r = await api("/api/challenges");
    const box = $("#adminlist");
    if (!r.ok) { box.textContent = errorText(r); return; }
    const list = Array.isArray(r.data) ? r.data : [];
    if (!list.length) { box.innerHTML = "<p>No challenges yet.</p>"; return; }
    box.innerHTML = `
      <table>
        <tr><th>ID</th><th>Title</th><th>Category</th><th class="num">Points</th><th></th></tr>
        ${list.map((c) => `
          <tr>
            <td>${c.id}</td><td>${esc(c.title)}</td><td>${esc(c.category)}</td><td class="num">${c.points}</td>
            <td><button data-edit="${c.id}">Edit</button> <button data-del="${c.id}">Delete</button></td>
          </tr>`).join("")}
      </table>`;
    box.querySelectorAll("[data-edit]").forEach((b) => (b.onclick = () => {
      const c = list.find((x) => x.id === Number(b.dataset.edit));
      f("cid").value = c.id;
      f("title").value = c.title;
      f("category").value = c.category;
      f("points").value = c.points;
      f("description").value = c.description;
      f("flag").value = "";
      f("flag").required = false;
      $("#flaghint").textContent = "(leave blank to keep the current flag)";
      $("#formtitle").textContent = `Edit challenge #${c.id}`;
      f("title").focus();
    }));
    box.querySelectorAll("[data-del]").forEach((b) => (b.onclick = async () => {
      if (!confirm("Delete this challenge?")) return;
      const r = await api(`/api/challenges/${b.dataset.del}`, { method: "DELETE" });
      if (!r.ok) return message(errorText(r), true);
      message("Challenge deleted.");
      loadList();
    }));
  };

  form.onsubmit = async (e) => {
    e.preventDefault();
    const body = {
      title: f("title").value.trim(),
      category: f("category").value.trim().toLowerCase(),
      points: Number(f("points").value),
      description: f("description").value,
      flag: f("flag").value.trim(),
    };
    const id = f("cid").value;
    const r = await api(id ? `/api/challenges/${id}` : "/api/challenges", { method: id ? "PUT" : "POST", body });
    if (!r.ok) return message(errorText(r), true);
    message(id ? "Challenge saved." : "Challenge created.");
    resetForm();
    loadList();
  };

  $("#st-refresh").onclick = () => loadStatus(1);
  $("#st-burst").onclick = () => loadStatus(20);
  loadList();
  loadStatus(1);
}

// ---------- admin: system status ----------
// Each service's /api/<service>/info endpoint is admin-only and says which pod
// answered. nginx adds X-Web-Pod (the frontend pod) to those responses only.
const SERVICES = ["auth", "challenges", "submissions", "scoreboard"];

async function loadStatus(requestsPerService) {
  const box = $("#sysstatus");
  box.textContent = "Loading...";
  const rows = [];
  const webPods = {};

  for (const svc of SERVICES) {
    const pods = {};
    let version = "-";
    let error = "";
    for (let i = 0; i < requestsPerService; i++) {
      const r = await api(`/api/${svc}/info`);
      if (!r.ok) { error = errorText(r); break; }
      pods[r.data.pod] = (pods[r.data.pod] || 0) + 1;
      version = r.data.version;
      const web = r.headers.get("X-Web-Pod");
      if (web) webPods[web] = (webPods[web] || 0) + 1;
    }
    rows.push({ svc, pods, version, error });
  }

  if (!$("#sysstatus")) return; // user left the page meanwhile
  const podList = (counts) =>
    Object.entries(counts).map(([pod, n]) => `${esc(pod)} (${n})`).join("<br>") || "-";
  box.innerHTML = `
    <table>
      <tr><th>Service</th><th>Version</th><th>Pods that answered (requests)</th></tr>
      ${rows.map((r) => `
        <tr><td>${r.svc}</td><td>${esc(r.version)}</td><td>${r.error ? esc(r.error) : podList(r.pods)}</td></tr>`).join("")}
      <tr><td>frontend</td><td>${esc(frontendVersion)}</td><td>${podList(webPods)}</td></tr>
    </table>`;
}

// ---------- router ----------
const routes = { challenges: pageChallenges, scoreboard: pageScoreboard, login: () => pageLogin(), admin: pageAdmin };

function route() {
  clearInterval(scoreTimer);
  const name = (location.hash.replace(/^#\/?/, "") || "challenges").split("/")[0];
  highlightNav(name);
  (routes[name] || pageChallenges)();
}

// ---------- boot ----------
// version.json sets the accent colour (so a rollout is visible) and the
// frontend version shown on the admin status table.
let frontendVersion = "-";
async function loadVersion() {
  try {
    const res = await fetch("/version.json", { cache: "no-store" });
    const v = await res.json();
    frontendVersion = v.version || "-";
    if (v.accent) document.documentElement.style.setProperty("--accent", v.accent);
  } catch { /* running without nginx, e.g. opened as a file */ }
}

// On navigation show the pending message (if any), otherwise clear the line.
window.addEventListener("hashchange", () => {
  message(...(pendingMsg || [""]));
  pendingMsg = null;
  route();
});
renderHeader();
loadVersion();
route();
