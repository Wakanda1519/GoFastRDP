(function () {
  "use strict";

  const COLLAPSE_KEY = "gofastrdp.collapsed";

  const state = {
    hosts: [],
    selection: null,
    query: "",
    clearPassword: false,
    collapsed: new Set(),
    drag: null,
  };

  const $ = (id) => document.getElementById(id);

  // Per-process token injected by the native host. Without it the bindings
  // reject the call, so a document loaded from anywhere else cannot use them.
  const API_TOKEN =
    (document.querySelector('meta[name="api-token"]') || {}).content || "";

  async function call(name, ...args) {
    const fn = window[name];
    if (typeof fn !== "function") {
      throw new Error("Метод " + name + " недоступен в этой среде");
    }
    return fn(API_TOKEN, ...args);
  }

  function toast(message, kind) {
    const palette =
      kind === "error"
        ? "border-red-200 bg-red-50 text-red-700"
        : "border-emerald-200 bg-emerald-50 text-emerald-700";
    const el = document.createElement("div");
    el.className =
      "rounded-lg border px-4 py-3 text-sm shadow-sm transition " + palette;
    el.textContent = message;
    $("toasts").appendChild(el);
    setTimeout(() => {
      el.style.opacity = "0";
      setTimeout(() => el.remove(), 250);
    }, 3200);
  }

  function describeError(err) {
    if (!err) return "Неизвестная ошибка";
    if (typeof err === "string") return err;
    return err.message || String(err);
  }

  function addr(host) {
    if (!host) return "";
    return host.host + (host.port && host.port !== 3389 ? ":" + host.port : "");
  }

  function hostCaption(host) {
    if (!host) return "";
    const parts = [];
    const name = (host.name || "").trim();
    const address = addr(host);
    if (name && name.toLowerCase() !== address.toLowerCase()) parts.push(name);
    if (address) parts.push(address);
    return parts.join(" · ");
  }

  function hostById(id) {
    return state.hosts.find((h) => h.id === id) || null;
  }

  function profileById(host, id) {
    if (!host || !id) return null;
    return (host.profiles || []).find((p) => p.id === id) || null;
  }

  function profileCount(n) {
    if (n === 0) return "нет профилей";
    if (n === 1) return "1 профиль";
    if (n >= 2 && n <= 4) return n + " профиля";
    return n + " профилей";
  }

  function loginLabel(profile) {
    if (!profile.username) {
      return profile.hasPassword ? "Пароль сохранен" : "Без учетных данных";
    }
    return profile.domain
      ? profile.domain + "\\" + profile.username
      : profile.username;
  }

  function matches(value, query) {
    return (value || "").toLowerCase().includes(query);
  }

  function loadCollapsed() {
    try {
      const raw = localStorage.getItem(COLLAPSE_KEY);
      if (!raw) return;
      const ids = JSON.parse(raw);
      if (Array.isArray(ids)) ids.forEach((id) => state.collapsed.add(id));
    } catch (err) {
      /* localStorage может быть недоступен */
    }
  }

  function saveCollapsed() {
    try {
      localStorage.setItem(COLLAPSE_KEY, JSON.stringify([...state.collapsed]));
    } catch (err) {
      /* localStorage может быть недоступен */
    }
  }

  function toggleCollapsed(hostId) {
    if (state.collapsed.has(hostId)) {
      state.collapsed.delete(hostId);
    } else {
      state.collapsed.add(hostId);
    }
    saveCollapsed();
    renderList();
  }

  function filterHosts() {
    const q = state.query.trim().toLowerCase();
    if (!q) {
      return state.hosts.map((host) => ({ host, profiles: host.profiles }));
    }
    const out = [];
    for (const host of state.hosts) {
      const hostMatch =
        matches(host.name, q) || matches(host.host, q) || matches(host.notes, q);
      const profiles = hostMatch
        ? host.profiles
        : host.profiles.filter(
            (p) =>
              matches(p.name, q) ||
              matches(p.username, q) ||
              matches(p.domain, q)
          );
      if (hostMatch || profiles.length > 0) {
        out.push({ host, profiles });
      }
    }
    return out;
  }

  function renderList() {
    const list = $("list");
    list.textContent = "";

    const groups = filterHosts();
    if (groups.length === 0) {
      const empty = document.createElement("div");
      empty.className = "px-3 py-6 text-center text-xs text-slate-500";
      empty.textContent =
        state.hosts.length === 0 ? "Групп пока нет" : "Ничего не найдено";
      list.appendChild(empty);
      return;
    }

    for (const group of groups) {
      list.appendChild(renderHostGroup(group.host, group.profiles));
    }
  }

  function renderHostGroup(host, visibleProfiles) {
    const container = document.createElement("div");
    container.className =
      "rounded-xl border border-transparent px-1 py-1 transition hover:border-slate-800";

    const header = document.createElement("div");
    header.className = "flex items-center gap-1 rounded-lg px-1 py-1 transition";
    if (state.selection && state.selection.hostId === host.id) {
      header.classList.add("bg-slate-800/50");
    }

    const collapsed = state.collapsed.has(host.id);

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className =
      "flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-slate-500 transition hover:bg-slate-800 hover:text-slate-200";
    toggle.title = collapsed ? "Развернуть группу" : "Свернуть группу";
    toggle.setAttribute("aria-label", toggle.title);
    toggle.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" class="h-3.5 w-3.5' +
      (collapsed ? "" : " rotate-90") +
      '"><path d="M9 6l6 6-6 6"/></svg>';
    toggle.addEventListener("click", (event) => {
      event.stopPropagation();
      toggleCollapsed(host.id);
    });
    header.appendChild(toggle);

    const open = document.createElement("button");
    open.type = "button";
    open.className = "min-w-0 flex-1 text-left";
    open.addEventListener("click", () => selectHost(host.id));

    const title = document.createElement("div");
    title.className = "truncate text-sm font-semibold text-slate-200";
    title.textContent = host.name;
    open.appendChild(title);

    const meta = document.createElement("div");
    meta.className = "mt-0.5 truncate text-[11px] text-slate-500";
    meta.textContent = addr(host) + " · " + profileCount(host.profiles.length);
    open.appendChild(meta);
    header.appendChild(open);

    const add = document.createElement("button");
    add.type = "button";
    add.className =
      "flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-slate-500 transition hover:bg-slate-700 hover:text-white";
    add.title = "Добавить профиль";
    add.setAttribute("aria-label", "Добавить профиль в группу " + host.name);
    add.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" class="h-3.5 w-3.5"><path d="M12 5v14M5 12h14"/></svg>';
    add.addEventListener("click", (event) => {
      event.stopPropagation();
      newProfile(host.id);
    });
    header.appendChild(add);

    container.appendChild(header);

    if (!collapsed) {
      const profiles = document.createElement("div");
      profiles.className = "mt-1 space-y-0.5 pl-6";

      if (visibleProfiles.length === 0) {
        const empty = document.createElement("div");
        empty.className =
          "flex items-center justify-between gap-2 rounded-lg px-2 py-2 text-[11px] text-slate-600";
        const text = document.createElement("span");
        text.textContent = "Нет профилей";
        empty.appendChild(text);
        const addSmall = document.createElement("button");
        addSmall.type = "button";
        addSmall.className =
          "rounded px-1.5 py-0.5 text-[11px] font-medium text-brand-400 transition hover:bg-slate-800 hover:text-brand-300";
        addSmall.textContent = "Добавить";
        addSmall.addEventListener("click", () => newProfile(host.id));
        empty.appendChild(addSmall);
        profiles.appendChild(empty);
      } else {
        for (const profile of visibleProfiles) {
          profiles.appendChild(renderProfileRow(host, profile));
        }
      }
      container.appendChild(profiles);
    }

    return container;
  }

  function renderProfileRow(host, profile) {
    const row = document.createElement("div");
    row.className =
      "conn-item group flex items-start gap-1.5" +
      (state.selection &&
      state.selection.kind === "profile" &&
      state.selection.hostId === host.id &&
      state.selection.profileId === profile.id
        ? " conn-item--active"
        : "");

    row.draggable = true;
    row.addEventListener("dragstart", (event) =>
      startProfileDrag(event, host.id, profile)
    );
    row.addEventListener("dragover", (event) =>
      dragOverProfile(event, host.id, profile)
    );
    row.addEventListener("dragleave", clearDropMarker);
    row.addEventListener("drop", (event) =>
      dropProfile(event, host.id, profile)
    );
    row.addEventListener("dragend", endProfileDrag);

    const star = document.createElement("button");
    star.type = "button";
    star.className =
      "mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-base leading-none transition " +
      (profile.favorite
        ? "text-amber-400 hover:text-amber-300"
        : "text-slate-500 hover:text-amber-300");
    star.textContent = profile.favorite ? "★" : "☆";
    star.title = profile.favorite ? "Убрать из избранного" : "В избранное";
    star.setAttribute("aria-label", star.title);
    star.addEventListener("click", (event) => {
      event.stopPropagation();
      toggleFavorite(host.id, profile.id);
    });
    row.appendChild(star);

    const main = document.createElement("button");
    main.type = "button";
    main.className = "min-w-0 flex-1 text-left";
    main.addEventListener("click", () => selectProfile(host.id, profile.id));

    const top = document.createElement("div");
    top.className = "flex items-center justify-between gap-2";

    const name = document.createElement("span");
    name.className = "truncate text-sm font-medium text-slate-100";
    name.textContent = profile.name;
    top.appendChild(name);

    main.appendChild(top);

    const sub = document.createElement("div");
    sub.className = "mt-0.5 truncate text-[11px] text-slate-400";
    sub.textContent = loginLabel(profile);
    main.appendChild(sub);

    row.appendChild(main);

    const handle = document.createElement("span");
    handle.className =
      "mt-1 flex h-5 w-4 shrink-0 cursor-grab items-center justify-center text-slate-600 opacity-0 transition group-hover:opacity-100";
    handle.title = "Перетащите, чтобы изменить порядок";
    handle.innerHTML =
      '<svg viewBox="0 0 10 16" fill="currentColor" class="h-4 w-3"><circle cx="2.5" cy="3" r="1.2"/><circle cx="7.5" cy="3" r="1.2"/><circle cx="2.5" cy="8" r="1.2"/><circle cx="7.5" cy="8" r="1.2"/><circle cx="2.5" cy="13" r="1.2"/><circle cx="7.5" cy="13" r="1.2"/></svg>';
    row.appendChild(handle);

    return row;
  }

  function startProfileDrag(event, hostId, profile) {
    state.drag = {
      hostId,
      profileId: profile.id,
      favorite: !!profile.favorite,
    };
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = "move";
      try {
        event.dataTransfer.setData("text/plain", profile.id);
      } catch (err) {
        /* некоторые WebView не отдают dataTransfer */
      }
    }
    event.currentTarget.classList.add("opacity-50");
  }

  function dragOverProfile(event, hostId, profile) {
    const drag = state.drag;
    if (!drag || drag.hostId !== hostId || drag.favorite !== !!profile.favorite) {
      return;
    }
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
    event.currentTarget.classList.add("conn-item--drop");
  }

  function clearDropMarker(event) {
    event.currentTarget.classList.remove("conn-item--drop");
  }

  function dropProfile(event, hostId, target) {
    event.preventDefault();
    event.currentTarget.classList.remove("conn-item--drop");

    const drag = state.drag;
    if (
      !drag ||
      drag.hostId !== hostId ||
      drag.favorite !== !!target.favorite ||
      drag.profileId === target.id
    ) {
      return;
    }

    const host = hostById(hostId);
    if (!host) return;

    const rect = event.currentTarget.getBoundingClientRect();
    const after = event.clientY > rect.top + rect.height / 2;

    const ids = host.profiles
      .map((p) => p.id)
      .filter((id) => id !== drag.profileId);
    let index = ids.indexOf(target.id);
    if (index < 0) return;
    if (after) index += 1;
    ids.splice(index, 0, drag.profileId);

    reorderProfiles(hostId, ids);
  }

  function endProfileDrag(event) {
    event.currentTarget.classList.remove("opacity-50");
    state.drag = null;
    document
      .querySelectorAll(".conn-item--drop")
      .forEach((el) => el.classList.remove("conn-item--drop"));
  }

  function isNewSelection() {
    const sel = state.selection;
    if (!sel) return true;
    if (sel.kind === "host") return !sel.hostId;
    return !sel.profileId;
  }

  function showEditor(kind) {
    const visible = !!kind;
    $("editor").classList.toggle("hidden", !visible);
    $("empty").classList.toggle("hidden", visible);
    $("host-card").classList.toggle("hidden", kind !== "host");
    $("session-card").classList.toggle("hidden", kind !== "host");
    $("profile-card").classList.toggle("hidden", kind !== "profile");
    $("btn-save").classList.toggle("hidden", !visible);
    $("btn-delete").classList.toggle("hidden", !visible || isNewSelection());
    $("btn-connect").classList.toggle(
      "hidden",
      kind !== "profile" || isNewSelection()
    );
    $("btn-add-profile").classList.toggle(
      "hidden",
      kind !== "host" || isNewSelection()
    );
    $("btn-delete").title =
      kind === "host" ? "Удалить группу" : "Удалить профиль";
  }

  function fillHostForm(host) {
    const h = host || {};
    $("f-host-name").value = h.name || "";
    $("f-host").value = h.host || "";
    $("f-port").value = h.port && h.port !== 3389 ? h.port : "";
    $("f-notes").value = h.notes || "";
    $("f-width").value = h.width || 1920;
    $("f-height").value = h.height || 1080;
    $("f-audio").value = String(h.audioMode ?? 0);
    $("f-fullscreen").checked = !!h.fullscreen;
    $("f-multimon").checked = !!h.multiMon;
    $("f-clipboard").checked = h.clipboard !== false;
  }

  function fillProfileForm(host, profile) {
    const p = profile || {};
    $("f-name").value = p.name || "";
    $("f-username").value = p.username || "";
    $("f-domain").value = p.domain || "";
    $("f-password").value = "";
    $("f-password").type = "password";
    $("btn-show-pass").textContent = "Показать";
    state.clearPassword = false;

    $("profile-host-info").textContent =
      hostCaption(host) + " · " + profileCount(host.profiles.length);

    if (profile) {
      $("pass-hint").textContent = p.hasPassword
        ? "Пароль сохранен и зашифрован. Введите новый, чтобы заменить."
        : "Пароль не сохранен — клиент запросит его при подключении.";
    } else {
      $("pass-hint").textContent =
        "Пароль шифруется средствами Windows и хранится только на этом компьютере.";
    }
  }

  function updateHeader() {
    const sel = state.selection;
    if (!sel) {
      $("detail-name").textContent = "Панель подключений";
      $("detail-sub").textContent =
        "Создайте группу с адресом и параметрами сеанса";
      return;
    }

    if (sel.kind === "host") {
      const host = sel.hostId ? hostById(sel.hostId) : null;
      if (!host) {
        $("detail-name").textContent = "Новая группа";
        $("detail-sub").textContent =
          "Укажите адрес и общие параметры сеанса";
        return;
      }
      $("detail-name").textContent = host.name;
      $("detail-sub").textContent =
        addr(host) + " · " + profileCount(host.profiles.length);
      return;
    }

    const host = hostById(sel.hostId);
    const profile = profileById(host, sel.profileId);
    if (!profile) {
      $("detail-name").textContent = "Новый профиль";
      $("detail-sub").textContent = host
        ? hostCaption(host)
        : "Выберите группу";
      return;
    }

    $("detail-name").textContent = profile.name;
    const parts = [hostCaption(host)];
    const username = (profile.username || "").trim();
    const profileName = (profile.name || "").trim();
    if (username && username.toLowerCase() !== profileName.toLowerCase()) {
      parts.push(username);
    }
    $("detail-sub").textContent = parts.filter(Boolean).join(" · ");
  }

  function selectHost(hostId) {
    const host = hostById(hostId);
    if (!host) return;
    if (state.collapsed.delete(hostId)) saveCollapsed();
    state.selection = { kind: "host", hostId };
    fillHostForm(host);
    updateHeader();
    showEditor("host");
    renderList();
  }

  function selectProfile(hostId, profileId) {
    const host = hostById(hostId);
    const profile = profileById(host, profileId);
    if (!host || !profile) return;
    if (state.collapsed.delete(hostId)) saveCollapsed();
    state.selection = { kind: "profile", hostId, profileId };
    fillProfileForm(host, profile);
    updateHeader();
    showEditor("profile");
    renderList();
  }

  function newHost() {
    state.selection = { kind: "host", hostId: null };
    state.clearPassword = false;
    fillHostForm(null);
    updateHeader();
    showEditor("host");
    renderList();
    $("f-host-name").focus();
  }

  function newProfile(hostId) {
    const host = hostById(hostId);
    if (!host) {
      toast("Сначала сохраните группу", "error");
      return;
    }
    if (state.collapsed.delete(hostId)) saveCollapsed();
    state.selection = { kind: "profile", hostId, profileId: null };
    fillProfileForm(host, null);
    updateHeader();
    showEditor("profile");
    renderList();
    $("f-name").focus();
  }

  function collectHost() {
    return {
      id:
        state.selection &&
        state.selection.kind === "host" &&
        state.selection.hostId
          ? state.selection.hostId
          : "",
      name: $("f-host-name").value.trim(),
      host: $("f-host").value.trim(),
      port: parseInt($("f-port").value, 10) || 3389,
      notes: $("f-notes").value,
      width: parseInt($("f-width").value, 10) || 1920,
      height: parseInt($("f-height").value, 10) || 1080,
      fullscreen: $("f-fullscreen").checked,
      multiMon: $("f-multimon").checked,
      clipboard: $("f-clipboard").checked,
      audioMode: parseInt($("f-audio").value, 10) || 0,
    };
  }

  function collectProfile(hostId) {
    const pw = $("f-password").value;
    let password = null;
    if (state.clearPassword) password = "";
    else if (pw !== "") password = pw;

    return {
      hostId,
      id:
        state.selection &&
        state.selection.kind === "profile" &&
        state.selection.profileId
          ? state.selection.profileId
          : "",
      name: $("f-name").value.trim(),
      username: $("f-username").value.trim(),
      domain: $("f-domain").value.trim(),
      password,
    };
  }

  async function refresh() {
    const previous = state.selection;
    try {
      state.hosts = (await call("api_list")) || [];
    } catch (err) {
      state.hosts = [];
      toast("Не удалось загрузить список: " + describeError(err), "error");
    }

    if (previous) {
      if (previous.kind === "host") {
        if (previous.hostId && !hostById(previous.hostId)) {
          state.selection = null;
        } else {
          state.selection = previous;
        }
      } else if (!previous.profileId) {
        state.selection = hostById(previous.hostId) ? previous : null;
      } else {
        const host = hostById(previous.hostId);
        state.selection = profileById(host, previous.profileId)
          ? previous
          : null;
      }
    }

    renderList();
  }

  async function loadInfo() {
    try {
      const info = await call("api_info");
      const button = $("btn-storage");
      button.title = "Открыть папку с данными: " + info.storePath;
      button.setAttribute(
        "aria-label",
        "Локальное хранилище: " + info.storePath
      );
    } catch (err) {
      /* информационная панель не критична */
    }
  }

  async function openDataFolder() {
    try {
      await call("api_open_data_folder");
    } catch (err) {
      toast("Не удалось открыть папку: " + describeError(err), "error");
    }
  }

  async function saveHost() {
    const payload = collectHost();
    if (!payload.host) {
      toast("Укажите адрес узла (IP или имя)", "error");
      $("f-host").focus();
      return;
    }
    try {
      const saved = await call("api_save_host", payload);
      state.selection = { kind: "host", hostId: saved.id };
      await refresh();
      const host = hostById(saved.id) || saved;
      state.selection = { kind: "host", hostId: host.id };
      fillHostForm(host);
      updateHeader();
      showEditor("host");
      toast("Группа сохранена");
    } catch (err) {
      toast("Не удалось сохранить группу: " + describeError(err), "error");
    }
  }

  async function saveProfile() {
    const sel = state.selection;
    if (!sel || sel.kind !== "profile" || !sel.hostId) return;
    const payload = collectProfile(sel.hostId);
    try {
      const saved = await call("api_save_profile", payload);
      state.selection = {
        kind: "profile",
        hostId: sel.hostId,
        profileId: saved.id,
      };
      await refresh();
      const host = hostById(sel.hostId);
      const profile = host ? profileById(host, saved.id) : null;
      if (host) fillProfileForm(host, profile);
      updateHeader();
      showEditor("profile");
      toast("Профиль сохранен");
    } catch (err) {
      toast("Не удалось сохранить профиль: " + describeError(err), "error");
    }
  }

  function save() {
    if (!state.selection) return;
    if (state.selection.kind === "host") {
      saveHost();
    } else {
      saveProfile();
    }
  }

  async function removeHost(hostId) {
    if (!hostId) return;
    const host = hostById(hostId);
    if (!host) return;
    const extra =
      host.profiles.length > 0
        ? " и все профили (" + host.profiles.length + ")"
        : "";
    if (!window.confirm("Удалить группу «" + host.name + "»" + extra + "?")) {
      return;
    }
    try {
      await call("api_delete_host", hostId);
      state.selection = null;
      await refresh();
      if (state.hosts.length > 0) {
        selectHost(state.hosts[0].id);
      } else {
        updateHeader();
        showEditor(null);
        renderList();
      }
      toast("Группа удалена");
    } catch (err) {
      toast("Не удалось удалить группу: " + describeError(err), "error");
    }
  }

  async function removeProfile(hostId, profileId) {
    const host = hostById(hostId);
    const profile = profileById(host, profileId);
    if (!host || !profile) return;
    if (!window.confirm("Удалить профиль «" + profile.name + "»?")) return;
    try {
      await call("api_delete_profile", hostId, profileId);
      await refresh();
      selectHost(hostId);
      toast("Профиль удален");
    } catch (err) {
      toast("Не удалось удалить профиль: " + describeError(err), "error");
    }
  }

  function remove() {
    const sel = state.selection;
    if (!sel) return;
    if (sel.kind === "host") {
      removeHost(sel.hostId);
    } else {
      removeProfile(sel.hostId, sel.profileId);
    }
  }

  async function connect() {
    const sel = state.selection;
    if (!sel || sel.kind !== "profile" || !sel.profileId) {
      toast("Сначала сохраните профиль", "error");
      return;
    }
    const btn = $("btn-connect");
    btn.disabled = true;
    try {
      await call("api_connect", sel.hostId, sel.profileId);
      toast("Сеанс запущен");
      await refresh();
    } catch (err) {
      toast("Не удалось подключиться: " + describeError(err), "error");
    } finally {
      btn.disabled = false;
    }
  }

  async function toggleFavorite(hostId, profileId) {
    try {
      await call("api_toggle_favorite", hostId, profileId);
      await refresh();
    } catch (err) {
      toast(
        "Не удалось изменить избранное: " + describeError(err),
        "error"
      );
    }
  }

  async function reorderProfiles(hostId, ids) {
    try {
      await call("api_reorder_profiles", hostId, ids);
      await refresh();
    } catch (err) {
      toast("Не удалось изменить порядок: " + describeError(err), "error");
      await refresh();
    }
  }

  function bindEvents() {
    $("search").addEventListener("input", (event) => {
      state.query = event.target.value;
      renderList();
    });
    $("btn-new-host").addEventListener("click", newHost);
    $("btn-empty-new").addEventListener("click", newHost);
    $("btn-save").addEventListener("click", save);
    $("btn-delete").addEventListener("click", remove);
    $("btn-connect").addEventListener("click", connect);
    $("btn-storage").addEventListener("click", openDataFolder);
    $("btn-edit-host").addEventListener("click", () => {
      if (state.selection && state.selection.hostId) {
        selectHost(state.selection.hostId);
      }
    });
    $("btn-add-profile").addEventListener("click", () => {
      if (state.selection && state.selection.hostId) {
        newProfile(state.selection.hostId);
      }
    });
    $("btn-show-pass").addEventListener("click", () => {
      const input = $("f-password");
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      $("btn-show-pass").textContent = show ? "Скрыть" : "Показать";
    });
    $("btn-clear-pass").addEventListener("click", () => {
      state.clearPassword = true;
      $("f-password").value = "";
      $("pass-hint").textContent =
        "Сохраненный пароль будет удален после сохранения.";
    });
    $("form").addEventListener("submit", (event) => event.preventDefault());
    document.addEventListener("keydown", (event) => {
      if (event.ctrlKey && event.key.toLowerCase() === "s") {
        event.preventDefault();
        save();
      }
    });

    // Navigate nowhere: any other document would inherit the native bindings.
    window.open = () => null;
    document.addEventListener("click", (event) => {
      const link = event.target.closest ? event.target.closest("a[href]") : null;
      if (link) event.preventDefault();
    });
    // Dropping a file or URL into the window would replace the page as well.
    for (const type of ["dragenter", "dragover", "drop"]) {
      document.addEventListener(type, (event) => event.preventDefault());
    }
  }

  // Tells the native host that the UI is rendered so it can replace the native
  // splash with the application window.
  function notifyReady() {
    try {
      if (typeof window.api_ready === "function") {
        window.api_ready(API_TOKEN);
      }
    } catch (err) {
      /* окно будет показано по таймеру в Go */
    }
  }

  function hidePreloader() {
    const el = $("preloader");
    if (el) el.remove();
  }

  async function init() {
    try {
      loadCollapsed();
      bindEvents();
      await loadInfo();
      await refresh();
      if (state.hosts.length > 0) {
        const first = state.hosts[0];
        if (first.profiles.length > 0) {
          selectProfile(first.id, first.profiles[0].id);
        } else {
          selectHost(first.id);
        }
      } else {
        state.selection = null;
        updateHeader();
        showEditor(null);
        renderList();
      }
    } finally {
      hidePreloader();
      notifyReady();
    }
  }

  window.addEventListener("DOMContentLoaded", init);
})();
