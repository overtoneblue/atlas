#!/usr/bin/env python3
"""atlas-hub — thin local service that assembles Atlas's workstream tree.

Reads the Hermes state.db (read-only) for session<->Discord bindings and the
Discord REST API (bot token) for server structure (categories/channels), then
serves one JSON tree mirroring the Discord shape:

    GET /health          -> liveness (no auth)
    GET /tree            -> the assembled tree (Bearer $API_SERVER_KEY)
    GET /channels        -> native shape store (?profile=, Bearer)
    POST /channels/{create|update|delete|assign} -> mutate it (Bearer)

Config via env (defaults match head):
    ATLAS_HUB_ENV      .env holding DISCORD_BOT_TOKEN / API_SERVER_KEY
                       (default: $HERMES_HOME/.env or ~/.hermes/.env)
    ATLAS_HUB_DB       state.db path (default: alongside the .env)
    ATLAS_HUB_PORT     listen port (default 8643, binds 127.0.0.1)
    ATLAS_HUB_PROFILE  sessions.profile_name filter (default: "default")
    ATLAS_HUB_CACHE    channel cache TTL seconds (default 300)

Stdlib only — run with any python3.
"""

import json
import os
import shutil
import sqlite3
import sys
import threading
import time
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DISCORD_API = "https://discord.com/api/v10"


def hermes_home() -> str:
    return os.environ.get("HERMES_HOME") or os.path.expanduser("~/.hermes")


def env_path(profile_home: str = "") -> str:
    if profile_home:
        return os.path.join(profile_home, ".env")
    return os.environ.get("ATLAS_HUB_ENV") or os.path.join(hermes_home(), ".env")


def read_env_file(path: str) -> dict:
    out = {}
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    out[k.strip()] = v.strip()
    except OSError:
        pass
    return out


ENV = read_env_file(env_path())
TOKEN = os.environ.get("DISCORD_BOT_TOKEN") or ENV.get("DISCORD_BOT_TOKEN", "")
AUTH = os.environ.get("API_SERVER_KEY") or ENV.get("API_SERVER_KEY", "")
PORT = int(os.environ.get("ATLAS_HUB_PORT", "8643"))
# Native shape store (categories/channels/assignments) — the one thing the
# hub OWNS rather than derives. systemd gives the service this StateDirectory.
STORE_PATH = os.environ.get("ATLAS_HUB_STORE") or "/var/lib/atlas-hub/channels.json"
CACHE_TTL = float(os.environ.get("ATLAS_HUB_CACHE", "300"))

# Friendlier channel names for non-discord session sources in the tree.
SOURCE_LABELS = {
    "cli": "CLI",
    "api_server": "API",
    "cron": "Cron",
    "telegram": "Telegram",
    "matrix": "Matrix",
    "webhook": "Webhook",
    "atlas": "Atlas",
}


def discover_profiles():
    """Profile registry: the hermes home (default) + every profiles/<name>/.

    Each entry carries its own state.db and its own Discord bot token (read
    from that profile's .env). Profiles without a state.db are skipped.
    ATLAS_HUB_PROFILES=default,debbie optionally narrows the registry.
    """
    home = hermes_home()
    reg = [{"name": "default", "home": home, "display": "Nolan"}]
    pdir = os.path.join(home, "profiles")
    if os.path.isdir(pdir):
        for name in sorted(os.listdir(pdir)):
            d = os.path.join(pdir, name)
            if name.startswith(".") or not os.path.isdir(d):
                continue
            reg.append({"name": name, "home": d, "display": name.capitalize()})
    out = []
    for p in reg:
        penv = read_env_file(os.path.join(p["home"], ".env"))
        p["token"] = penv.get("DISCORD_BOT_TOKEN") or (TOKEN if p["name"] == "default" else "")
        p["db"] = (os.environ.get("ATLAS_HUB_DB") if p["name"] == "default" else "") or os.path.join(p["home"], "state.db")
        p["stats"] = os.path.join(p["home"], "stats")
        if os.path.exists(p["db"]):
            out.append(p)
    filt = [s.strip() for s in os.environ.get("ATLAS_HUB_PROFILES", "").split(",") if s.strip()]
    if filt:
        out = [p for p in out if p["name"] in filt]
    return out


PROFILES = discover_profiles()

# ---- native shape store -----------------------------------------------------

_STORE_LOCK = threading.Lock()


def _load_store():
    try:
        with open(STORE_PATH) as f:
            doc = json.load(f)
        if isinstance(doc, dict):
            doc.setdefault("version", 1)
            doc.setdefault("profiles", {})
            return doc
    except (OSError, ValueError):
        pass
    return {"version": 1, "profiles": {}}


def _save_store(doc):
    d = os.path.dirname(STORE_PATH)
    if d:
        os.makedirs(d, exist_ok=True)
    tmp = STORE_PATH + ".tmp"
    with open(tmp, "w") as f:
        json.dump(doc, f, indent=1, sort_keys=True)
    os.replace(tmp, STORE_PATH)


def _dismissed_get():
    """Delegation ids hidden from the spawned list (their ledger rows live in
    Hermes state.db, which stays read-only; this is the hub's own memory)."""
    doc = _load_store()
    return set(x for x in (doc.get("dismissed") or []) if isinstance(x, str))


def _dismissed_add(did):
    with _STORE_LOCK:
        doc = _load_store()
        lst = [x for x in (doc.get("dismissed") or []) if x != did]
        lst.append(did)
        doc["dismissed"] = lst[-400:]
        _save_store(doc)


def _pstore(doc, profile):
    return doc.setdefault("profiles", {}).setdefault(
        profile or "default", {"categories": [], "channels": [], "assign": {}})


def _store_payload(profile):
    with _STORE_LOCK:
        ps = _pstore(_load_store(), profile)
    return {"profile": profile or "default", "categories": ps["categories"],
            "channels": ps["channels"], "assign": ps["assign"]}


def _clean_name(v):
    s = str(v or "").strip()
    if not s:
        raise ValueError("name is required")
    return s[:80]


def _clean_template(v):
    return str(v or "")[:8000]


def _find(lst, nid):
    return next((x for x in lst if x.get("id") == nid), None)


_ANY = object()


def _name_taken(lst, name, exclude=None, cat=_ANY):
    """Case-insensitive name collision within one scope: categories are unique
    per profile, channels per category."""
    low = name.casefold()
    return any(
        x.get("id") != exclude and str(x.get("name", "")).casefold() == low
        and (cat is _ANY or (x.get("category_id") or None) == cat)
        for x in lst)


def channels_mutate(action, body):
    """One mutation against the store, under the lock, atomically saved.
    Raises ValueError for bad input (handler maps it to HTTP 400)."""
    profile = str(body.get("profile") or "default")
    with _STORE_LOCK:
        doc = _load_store()
        ps = _pstore(doc, profile)
        cats, chans, assign = ps["categories"], ps["channels"], ps["assign"]
        if action == "create":
            kind = body.get("kind")
            if kind == "category":
                name = _clean_name(body.get("name"))
                if _name_taken(cats, name):
                    raise ValueError(f'a category named "{name}" already exists')
                item = {"id": "cat-" + uuid.uuid4().hex[:6], "name": name, "order": len(cats)}
                cats.append(item)
            elif kind == "channel":
                cid = body.get("category_id") or None
                if cid and not _find(cats, cid):
                    raise ValueError("category_id not found")
                name = _clean_name(body.get("name"))
                if _name_taken(chans, name, cat=cid):
                    raise ValueError(f'a channel named "{name}" already exists in this category')
                item = {"id": "chan-" + uuid.uuid4().hex[:6], "category_id": cid, "name": name,
                        "template": _clean_template(body.get("template")), "order": len(chans)}
                chans.append(item)
            else:
                raise ValueError("kind must be category|channel")
            _save_store(doc)
            return {"ok": True, "item": item}
        if action == "update":
            kind, nid = body.get("kind"), str(body.get("id") or "")
            lst = cats if kind == "category" else chans if kind == "channel" else None
            if lst is None:
                raise ValueError("kind must be category|channel")
            item = _find(lst, nid)
            if not item:
                raise ValueError("not found")
            new_name = _clean_name(body.get("name")) if "name" in body else item["name"]
            new_cat = item.get("category_id") or None
            if kind == "channel" and "category_id" in body:
                new_cat = body.get("category_id") or None
                if new_cat and not _find(cats, new_cat):
                    raise ValueError("category_id not found")
            moved = kind == "channel" and new_cat != (item.get("category_id") or None)
            # Only a CHANGED name/scope can collide: an untouched legacy duplicate stays editable.
            if (new_name.casefold() != item["name"].casefold() or moved) and _name_taken(
                    lst, new_name, exclude=nid, cat=new_cat if kind == "channel" else _ANY):
                where = " in that category" if kind == "channel" else ""
                raise ValueError(f'a {kind} named "{new_name}" already exists{where}')
            item["name"] = new_name
            if kind == "channel":
                if "template" in body:
                    item["template"] = _clean_template(body.get("template"))
                item["category_id"] = new_cat
            if "order" in body:
                try:
                    item["order"] = int(body.get("order"))
                except (TypeError, ValueError):
                    raise ValueError("order must be an integer")
            _save_store(doc)
            return {"ok": True, "item": item}
        if action == "delete":
            kind, nid = body.get("kind"), str(body.get("id") or "")
            if kind == "category":
                item = _find(cats, nid)
                if not item:
                    raise ValueError("not found")
                cats.remove(item)
                orphans = 0
                for ch in chans:
                    if ch.get("category_id") == nid:
                        ch["category_id"] = None
                        orphans += 1
                _save_store(doc)
                return {"ok": True, "deleted": nid, "orphaned": orphans}
            if kind == "channel":
                item = _find(chans, nid)
                if not item:
                    raise ValueError("not found")
                chans.remove(item)
                ps["assign"] = {k: v for k, v in assign.items() if v != nid}
                _save_store(doc)
                return {"ok": True, "deleted": nid,
                        "detached": len(assign) - len(ps["assign"])}
            raise ValueError("kind must be category|channel")
        if action == "assign":
            sid = str(body.get("session") or "").strip()
            if not sid:
                raise ValueError("session is required")
            cid = body.get("channel_id") or None
            if cid is not None:
                if not _find(chans, cid):
                    raise ValueError("channel_id not found")
                assign[sid] = cid
            else:
                assign.pop(sid, None)
            _save_store(doc)
            return {"ok": True, "session": sid, "channel_id": cid}
        raise ValueError("unknown action")


_cache = {
    "guilds": {},          # profile -> guilds
    "guilds_at": {},
    "channels": {},        # (profile, guild_id) -> {channel_id: channel_obj}
    "channels_at": {},
    "thread_parent": {},   # (profile, thread_id) -> parent channel_id (or None)
}


def discord_get(path: str, token: str):
    req = urllib.request.Request(
        DISCORD_API + path,
        headers={"Authorization": f"Bot {token}", "User-Agent": "atlas-hub/0.1"},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def get_guilds(prof):
    key = prof["name"]
    now = time.time()
    if key not in _cache["guilds"] or now - _cache["guilds_at"].get(key, 0.0) > CACHE_TTL:
        _cache["guilds"][key] = discord_get("/users/@me/guilds", prof["token"])
        _cache["guilds_at"][key] = now
    return _cache["guilds"][key]


def channel_map(guild_id: str, prof):
    key = (prof["name"], guild_id)
    now = time.time()
    if key not in _cache["channels"] or now - _cache["channels_at"].get(key, 0.0) > CACHE_TTL:
        chans = discord_get(f"/guilds/{guild_id}/channels", prof["token"])
        _cache["channels"][key] = {c["id"]: c for c in chans}
        _cache["channels_at"][key] = now
    return _cache["channels"][key]


def resolve_thread_parent(thread_id: str, prof):
    """Threads are not listed in guild channels; fetch once and cache."""
    key = (prof["name"], thread_id)
    if key in _cache["thread_parent"]:
        return _cache["thread_parent"][key]
    try:
        c = discord_get(f"/channels/{thread_id}", prof["token"])
        parent = c.get("parent_id")
    except Exception:
        parent = None
    _cache["thread_parent"][key] = parent
    return parent


def resolve_location(chat_id: str, thread_id: str, prof):
    """Return (guild_id, channel_obj, category_obj) for a session, or None."""
    for g in get_guilds(prof):
        cmap = channel_map(g["id"], prof)
        # direct channel session
        if chat_id and chat_id in cmap:
            chan = cmap[chat_id]
            cat = cmap.get(chan.get("parent_id") or "", None)
            return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
        # thread session: thread_id -> parent channel
        if thread_id:
            parent = resolve_thread_parent(thread_id, prof)
            if parent and parent in cmap:
                chan = cmap[parent]
                cat = cmap.get(chan.get("parent_id") or "", None)
                return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
    return None


def find_session(session_id: str):
    """(profile, row) for a session id across every profile db, else (None, None)."""
    for prof in PROFILES:
        con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
        con.row_factory = sqlite3.Row
        try:
            row = con.execute(
                "SELECT source, thread_id, chat_id FROM sessions WHERE id=?", (session_id,)
            ).fetchone()
        finally:
            con.close()
        if row is not None:
            return prof, row
    return None, None


def session_route(profile: str, session_id: str):
    """Read-only view of one session's persisted runtime routes.

    Hermes keeps two shapes in sessions.model_config: the top-level
    provider/base_url/api_mode every desktop/TUI/Atlas model switch writes,
    and the nested gateway_runtime the messaging gateway writes per turn.
    Resume prefers the nested one, so a chat that last ran in Discord on
    provider X and was then switched in Atlas comes back as (Atlas model,
    provider X). atlasd reads this to detect and heal exactly that pairing.
    """
    profs = [p for p in PROFILES if p["name"] == (profile or "default")] or PROFILES
    for prof in profs:
        con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
        con.row_factory = sqlite3.Row
        try:
            row = con.execute(
                "SELECT source, model, model_config FROM sessions WHERE id=?", (session_id,)
            ).fetchone()
        finally:
            con.close()
        if row is None:
            continue
        try:
            mc = json.loads(row["model_config"] or "{}")
        except Exception:
            mc = {}
        if not isinstance(mc, dict):
            mc = {}
        nested = mc.get("gateway_runtime") if isinstance(mc.get("gateway_runtime"), dict) else {}

        def side(d, model=""):
            out: dict = {k: str(d.get(k) or "") for k in ("provider", "base_url", "api_mode") if d.get(k)}
            if model:
                out["model"] = model
            if d.get("fallback_active"):
                out["fallback_active"] = True
            return out

        rc = mc.get("reasoning_config") if isinstance(mc.get("reasoning_config"), dict) else {}
        reasoning = ""
        if rc:
            reasoning = "none" if rc.get("enabled") is False else str(rc.get("effort") or "")
        return {
            "ok": True,
            "profile": prof["name"],
            "session": session_id,
            "source": row["source"] or "",
            "model": row["model"] or "",
            "reasoning": reasoning,
            "top": side(mc, str(mc.get("model") or "")),
            "nested": side(nested),
        }
    return {"ok": False, "error": "unknown session"}


def load_sessions(prof, include_hidden=False):
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        # Each db belongs to one profile; the default db additionally carries
        # legacy NULL-profile rows (pre-multiprofile sessions of the same profile).
        where = "archived=0 AND (profile_name=? OR profile_name IS NULL)"
        if not include_hidden:
            where = "hidden=0 AND " + where
        return con.execute(
            "SELECT id, source, chat_id, chat_type, thread_id, display_name, title, "
            "last_activity_at, message_count, pinned, hidden "
            "FROM sessions WHERE " + where + " "
            "ORDER BY last_activity_at DESC",
            (prof["name"],),
        ).fetchall()
    finally:
        con.close()


def post_node(row, prof):
    return {
        "kind": "post",
        "name": row["title"] or row["display_name"] or row["id"],
        "session_id": row["id"],
        "profile": prof["name"],
        "chat_type": row["chat_type"],
        "last_active": row["last_activity_at"] or 0,
        "message_count": row["message_count"] or 0,
        "pinned": bool(row["pinned"]),
        "hidden": bool(row["hidden"]),
        "source": row["source"],
    }


def build_tree(include_hidden=False):
    errors = []
    sections = []
    chan_store = _load_store()
    for prof in PROFILES:
        try:
            node = build_profile_section(prof, errors, include_hidden, chan_store)
        except Exception as e:
            errors.append(f"profile {prof['name']}: {e}")
            continue
        if node is not None:
            sections.append(node)
    return {"generated_at": time.time(), "sections": sections, "errors": errors}


def build_profile_section(prof, errors, include_hidden=False, chan_store=None):
    """One profile's subtree: native channels, discord guilds, other sources."""
    rows = load_sessions(prof, include_hidden)
    discord_rows = [r for r in rows if r["source"] == "discord"]
    other_rows = [r for r in rows if r["source"] != "discord"]

    children = []

    # ---- Atlas-native channels: categories -> channels -> assigned posts.
    # The hub OWNS this shape (channels.json); nothing here touches Discord.
    # A post's assignment wins over its source grouping; only non-discord
    # rows can be assigned (Discord rows keep their guild view). Native
    # categories/channels render even with zero chats, so they are visible
    # the moment they are created.
    ps = ((chan_store or {}).get("profiles") or {}).get(prof["name"]) or {}
    assign = ps.get("assign") or {}
    chans = ps.get("channels") or []
    valid = {c["id"] for c in chans if c.get("id")}
    by_chan = {}
    for r in other_rows:
        cid = assign.get(r["id"])
        if cid and cid in valid:
            p = post_node(r, prof)
            p["channel_id"] = cid
            by_chan.setdefault(cid, []).append(p)
    if by_chan:
        other_rows = [r for r in other_rows if assign.get(r["id"]) not in valid]
    chans_by_cat = {}
    for ch in sorted(chans, key=lambda c: (c.get("order", 0), c.get("name", ""))):
        node = {"kind": "channel", "name": ch.get("name") or ch["id"], "id": ch["id"],
                "native": True, "profile": prof["name"], "template": ch.get("template") or "",
                "category_id": ch.get("category_id"),
                "children": sorted(by_chan.get(ch["id"], []), key=lambda p: -p["last_active"])}
        chans_by_cat.setdefault(ch.get("category_id") or "", []).append(node)
    for cat in sorted(ps.get("categories") or [], key=lambda c: (c.get("order", 0), c.get("name", ""))):
        children.append({"kind": "category", "name": cat.get("name") or cat["id"], "id": cat["id"],
                         "native": True, "profile": prof["name"],
                         "children": chans_by_cat.pop(cat["id"], [])})
    children.extend(chans_by_cat.get("", []))  # channels without a category sit at profile level

    # ---- Discord side: guild -> category -> channel -> posts
    guilds_out = {}  # guild_id -> {name, cats: {name: {chans: {name: [posts]}}}}
    unfiled = []
    for r in discord_rows:
        try:
            loc = resolve_location(r["chat_id"], r["thread_id"], prof)
        except Exception as e:
            errors.append(f"resolve {r['id']}: {e}")
            loc = None
        if not loc:
            unfiled.append(post_node(r, prof))
            continue
        gid, chan, cat = loc
        gname = next((g["name"] for g in get_guilds(prof) if g["id"] == gid), gid)
        g = guilds_out.setdefault(gid, {"name": gname, "cats": {}})
        ckey = cat["name"] if cat else "Unfiled"
        corder = cat.get("position", 99) if cat else 9999
        c = g["cats"].setdefault(ckey, {"order": corder, "chans": {}})
        ch = c["chans"].setdefault(chan["name"], {"order": chan.get("position", 99), "posts": []})
        ch["posts"].append(post_node(r, prof))

    for gid, g in sorted(guilds_out.items()):
        cat_nodes = []
        for cname in sorted(g["cats"], key=lambda k: (g["cats"][k]["order"], k)):
            c = g["cats"][cname]
            chan_nodes = []
            for chname in sorted(c["chans"], key=lambda k: (c["chans"][k]["order"], k)):
                posts = sorted(c["chans"][chname]["posts"], key=lambda p: -p["last_active"])
                if posts:
                    chan_nodes.append({"kind": "channel", "name": chname, "children": posts})
            if chan_nodes:
                cat_nodes.append({"kind": "category", "name": cname, "children": chan_nodes})
        if cat_nodes:
            children.append({"kind": "guild", "name": g["name"], "children": cat_nodes})

    # ---- Other sources: one category, one channel per source
    if other_rows:
        by_source = {}
        for r in other_rows:
            by_source.setdefault(r["source"], []).append(post_node(r, prof))
        chan_nodes = [
            {"kind": "channel", "name": SOURCE_LABELS.get(src, src), "children": sorted(posts, key=lambda p: -p["last_active"])}
            for src, posts in sorted(by_source.items())
        ]
        children.append({"kind": "category", "name": "Other sessions", "children": chan_nodes})

    if unfiled:
        children.append({
            "kind": "category",
            "name": "Unfiled",
            "children": [{"kind": "channel", "name": "unknown", "children": unfiled}],
        })

    if not children:
        return None
    return {"kind": "profile", "name": prof["display"], "profile": prof["name"], "children": children}


# ---- spawned work: subagent runs + pi tasks, nestable under their chat ----
#
# Sources, cheapest first:
#   · async_delegations (state.db, read-only) — live state + parent linkage
#     (parent_session_id, or a thread id inside origin_session)
#   · cache/delegation/live/<deleg_id>/manifest.json — goals + log paths
#     (7-day retention; survives ledger cleanup)
#   · pi task quartet (spec/log/status/meta) — parent stamped by pi-task
#     when dispatched from an agent session
# Every item resolves its parent to a session id when possible; the app
# drops items it cannot place under a chat.

SPAWN_STATES = {
    "running": "running", "finalizing": "running",
    "completed": "done", "failed": "failed", "error": "failed",
}


def _live_dir_root(prof):
    return os.path.join(prof["home"], "cache", "delegation", "live")


def _thread_from_origin(origin):
    if not origin or "thread:" not in origin:
        return ""
    return origin.split("thread:", 1)[1].split(":", 1)[0].strip()


def _session_for_thread(prof, thread_id):
    if not thread_id:
        return ""
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        row = con.execute(
            "SELECT id FROM sessions WHERE thread_id=? ORDER BY last_activity_at DESC LIMIT 1",
            (thread_id,),
        ).fetchone()
        return row["id"] if row else ""
    finally:
        con.close()


def _load_delegations(prof):
    """Recent async-delegation ledger rows for one profile."""
    since = time.time() - 8 * 24 * 3600
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        return con.execute(
            "SELECT delegation_id, state, origin_session, parent_session_id, task_json, "
            "dispatched_at, completed_at FROM async_delegations "
            "WHERE updated_at >= ? ORDER BY updated_at DESC LIMIT 120",
            (since,),
        ).fetchall()
    finally:
        con.close()


def _tasks_from_manifest(m):
    out = []
    for t in (m.get("tasks") or []):
        if isinstance(t, dict):
            out.append({
                "i": int(t.get("index") or 0),
                "goal": str(t.get("goal") or ""),
                "status": str(t.get("status") or ""),
            })
    return out


def _read_kv(path):
    out = {}
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    out[k.strip()] = v.strip()
    except OSError:
        pass
    return out


def _parse_ts(s):
    """ISO8601 (optionally tz-aware) or '%Y-%m-%d %H:%M:%S' -> epoch, else 0."""
    if not s:
        return 0.0
    try:
        from datetime import datetime
        if "T" in s:
            return datetime.fromisoformat(s).timestamp()
        return datetime.strptime(s, "%Y-%m-%d %H:%M:%S").timestamp()
    except ValueError:
        return 0.0


def _pi_tasks_dir():
    return os.environ.get("ATLAS_PI_TASKS") or "/mnt/cache/appdata/pi/tasks"


def spawn_items():
    """Every recent spawned run across profiles, each with its parent chat
    resolved to a session id when it can be."""
    items, errors = [], []

    dismissed = _dismissed_get()
    for prof in PROFILES:
        seen = set()
        # -- ledger rows (live state + parent) --------------------------
        try:
            rows = _load_delegations(prof)
        except sqlite3.Error:
            rows = []
        dirs = {}
        try:
            for name in os.listdir(_live_dir_root(prof)):
                if name.startswith("deleg_"):
                    d = {}
                    mpath = os.path.join(_live_dir_root(prof), name, "manifest.json")
                    try:
                        with open(mpath) as f:
                            d = json.load(f)
                    except (OSError, ValueError):
                        pass
                    dirs[name] = d
        except OSError:
            pass

        for r in rows:
            did = r["delegation_id"]
            if did in dismissed:
                continue
            seen.add(did)
            parent = r["parent_session_id"] or ""
            pchat = ""
            if not parent:
                pchat = _thread_from_origin(r["origin_session"] or "")
                parent = _session_for_thread(prof, pchat) if pchat else ""
            tasks = _tasks_from_manifest(dirs.get(did) or {})
            goals = []
            if r["task_json"]:
                try:
                    tj = json.loads(r["task_json"])
                    goals = [g for g in (tj.get("goals") or []) if isinstance(g, str)]
                except ValueError:
                    pass
            items.append({
                "kind": "subagent", "id": did, "profile": prof["name"],
                "parent": parent, "parent_chat": pchat,
                "state": SPAWN_STATES.get(r["state"], "unknown"),
                "title": (goals or [tasks[0]["goal"] if tasks else did])[0],
                "started": r["dispatched_at"] or 0, "completed": r["completed_at"] or 0,
                "tasks": max(len(goals), len(tasks), 1), "has_log": bool(tasks),
            })

        # -- live dirs the ledger no longer remembers -------------------
        for did, m in dirs.items():
            if did in seen or did in dismissed:
                continue
            tasks = _tasks_from_manifest(m)
            items.append({
                "kind": "subagent", "id": did, "profile": prof["name"],
                "parent": "", "parent_chat": "",
                "state": "done" if m.get("completed") else "unknown",
                "title": tasks[0]["goal"] if tasks else did,
                "started": _parse_ts(m.get("started") or ""),
                "completed": _parse_ts(m.get("completed") or ""),
                "tasks": max(len(tasks), 1), "has_log": bool(tasks),
            })

    # -- pi tasks (one shared dir; parents stamped by pi-task) ----------
    try:
        pdir = _pi_tasks_dir()
        for name in sorted(os.listdir(pdir)):
            if not (name.startswith("pi-") and name.endswith(".meta")):
                continue
            tid = name[:-5]
            meta = _read_kv(os.path.join(pdir, name))
            parent = meta.get("parent_session", "")
            pchat = meta.get("parent_chat", "")
            if not parent and pchat:
                for prof in PROFILES:
                    parent = _session_for_thread(prof, pchat)
                    if parent:
                        break
            rc = None
            if os.path.isfile(os.path.join(pdir, tid + ".status")):
                try:
                    with open(os.path.join(pdir, tid + ".status")) as f:
                        rc = int(f.read().strip() or "0")
                except (OSError, ValueError):
                    rc = 1
            items.append({
                "kind": "pi", "id": tid, "profile": "default",
                "parent": parent, "parent_chat": pchat,
                "state": "running" if rc is None else ("done" if rc == 0 else "failed"),
                "title": meta.get("title") or tid,
                "started": _parse_ts(meta.get("created") or ""),
                "completed": 0, "tasks": 1, "has_log": True, "rc": rc,
            })
    except OSError as e:
        errors.append(f"pi tasks: {e}")

    # -- debbie dispatches (debbie-task wrapper; parent stamped like pi) ----
    for prof in PROFILES:
        ddir = os.path.join(prof["home"], "cache", "debbie-tasks")
        try:
            names = sorted(os.listdir(ddir))
        except OSError:
            continue
        for name in names:
            if not (name.startswith("debb-") and name.endswith(".meta")):
                continue
            tid = name[:-5]
            meta = _read_kv(os.path.join(ddir, name))
            parent = meta.get("parent_session", "")
            pchat = meta.get("parent_chat", "")
            if not parent and pchat:
                parent = _session_for_thread(prof, pchat)
            state, rc, completed = "running", None, 0.0
            spath = os.path.join(ddir, tid + ".status")
            try:
                with open(spath) as f:
                    rc = int((f.read().strip() or "0"))
                state = "done" if rc == 0 else "failed"
                completed = os.path.getmtime(spath)
            except (OSError, ValueError):
                pass
            try:
                started = float(meta.get("started", "") or 0)
            except ValueError:
                started = _parse_ts(meta.get("started", ""))
            items.append({
                "kind": "debbie", "id": tid, "profile": prof["name"],
                "parent": parent, "parent_chat": pchat, "state": state,
                "title": meta.get("title", "") or tid,
                "started": started, "completed": completed, "tasks": 1,
                "has_log": os.path.isfile(os.path.join(ddir, tid + ".log")), "rc": rc,
            })

    items.sort(key=lambda it: -(it.get("started") or 0))
    return {"items": items, "errors": errors}


def spawn_log(kind, sid, task, lines):
    """Tail one spawned run's live log. Paths stay inside the two known
    roots — the caller-supplied id is validated, never joined raw."""
    lines = max(1, min(2000, lines or 400))
    path = ""
    if sid.startswith("pi-") and all(c.isalnum() or c == "-" for c in sid):
        path = os.path.join(_pi_tasks_dir(), sid + ".log")
    elif kind == "debbie" and sid.startswith("debb-") and all(c.isalnum() or c == "-" for c in sid):
        for prof in PROFILES:
            cand = os.path.join(prof["home"], "cache", "debbie-tasks", sid + ".log")
            if os.path.isfile(cand):
                path = cand
                break
    elif kind == "subagent" and sid.startswith("deleg_") and all(c.isalnum() or c == "_" for c in sid):
        for prof in PROFILES:
            cand = os.path.join(_live_dir_root(prof), sid, "task-%d.log" % max(0, task or 0))
            if os.path.isfile(cand):
                path = cand
                break
    if not path or not os.path.isfile(path):
        return None
    with open(path, "rb") as f:
        f.seek(0, os.SEEK_END)
        size = f.tell()
        f.seek(max(0, size - 262144))
        data = f.read().decode("utf-8", "replace")
    return "\n".join(data.splitlines()[-lines:])


def _unlink_set(d, tid):
    n = 0
    for name in os.listdir(d):
        if name.startswith(tid + "."):
            fp = os.path.join(d, name)
            if os.path.isfile(fp):
                os.unlink(fp)
                n += 1
    return n


def spawn_delete(kind, id, profile):
    """Delete one spawned run's record. debbie/pi: their task files, refused
    while still running. subagent: the hub's own live-dir + a dismissal —
    Hermes' ledger row is never touched, it just stops being surfaced."""
    if not (id and len(id) <= 80 and all(c.isalnum() or c in "-_" for c in id)):
        raise ValueError("bad id")
    if kind == "debbie":
        prof = next((p for p in PROFILES if p["name"] == (profile or "default")), None)
        if not prof or not id.startswith("debb-"):
            raise ValueError("unknown debbie task")
        ddir = os.path.join(prof["home"], "cache", "debbie-tasks")
        present = [n for n in os.listdir(ddir) if n.startswith(id + ".")] if os.path.isdir(ddir) else []
        if not present:
            raise ValueError("not found")
        if id + ".status" not in present:
            raise ValueError("still running — wait for it to finish")
        return {"ok": True, "kind": kind, "id": id, "removed": _unlink_set(ddir, id)}
    if kind == "pi":
        if not id.startswith("pi-"):
            raise ValueError("unknown pi task")
        pdir = _pi_tasks_dir()
        present = [n for n in os.listdir(pdir) if n.startswith(id + ".")] if os.path.isdir(pdir) else []
        if not present:
            raise ValueError("not found")
        if id + ".status" not in present:
            raise ValueError("still running — wait for it to finish")
        return {"ok": True, "kind": kind, "id": id, "removed": _unlink_set(pdir, id)}
    if kind == "subagent":
        if not id.startswith("deleg_"):
            raise ValueError("unknown delegation")
        prof = next((p for p in PROFILES if p["name"] == (profile or "default")), None)
        if not prof:
            raise ValueError("unknown profile")
        try:
            rows = _load_delegations(prof)
        except sqlite3.Error:
            rows = []
        for r in rows:
            if r["delegation_id"] == id and SPAWN_STATES.get(r["state"]) == "running":
                raise ValueError("still running — wait for it to finish")
        root = os.path.realpath(_live_dir_root(prof))
        target = os.path.realpath(os.path.join(root, id))
        removed = 0
        if target.startswith(root + os.sep) and os.path.isdir(target):
            shutil.rmtree(target)
            removed = 1
        _dismissed_add(id)
        return {"ok": True, "kind": kind, "id": id, "removed": removed, "dismissed": True}
    raise ValueError("unknown kind")


# ---- mirror: relay atlas-originated messages into discord threads ----

def _post_discord_message(channel_id: str, content: str, token: str):
    payload = json.dumps({"content": content}).encode()
    req = urllib.request.Request(
        f"{DISCORD_API}/channels/{channel_id}/messages",
        data=payload,
        method="POST",
        headers={
            "Authorization": f"Bot {token}",
            "Content-Type": "application/json",
            "User-Agent": "atlas-hub/0.1",
        },
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def _chunks(text: str, limit: int = 1900):
    out, cur = [], ""
    for line in text.split("\n"):
        if len(line) > limit:
            if cur:
                out.append(cur)
                cur = ""
            for i in range(0, len(line), limit):
                out.append(line[i:i + limit])
            continue
        if cur and len(cur) + len(line) + 1 > limit:
            out.append(cur)
            cur = ""
        cur = cur + ("\n" if cur else "") + line
    if cur:
        out.append(cur)
    return out or [""]


def mirror_message(session_id: str, role: str, content: str):
    """Post one message into the session's Discord thread (if it has one).

    The posts are made by the session's own profile bot; each gateway
    hard-drops its own bot's messages on ingest, so mirroring can never
    re-trigger a turn.
    """
    prof, row = find_session(session_id)
    if prof is None or row is None:
        return {"ok": False, "error": "unknown session"}
    if row["source"] != "discord" or not row["thread_id"]:
        return {"ok": False, "error": "no discord thread for session"}
    target = row["thread_id"]
    if role == "user":
        quoted = "\n".join("> " + ln for ln in (content or "").split("\n"))
        text = f"-# ⌨️ atlas — Overtoneblue\n{quoted}"
    else:
        text = content or ""
    ids = []
    for chunk in _chunks(text):
        if not chunk.strip():
            continue
        resp = _post_discord_message(target, chunk, prof["token"])
        ids.append(resp.get("id"))
    return {"ok": True, "chunks": len(ids), "message_ids": ids}


# ---- native-parity turn mirror: reasoning, tool lines, reply, stats card ----

_TOOL_EMOJIS = {
    "terminal": "💻", "close_terminal": "🖥️", "read_terminal": "🖥️",
    "patch": "🔧", "read_file": "📖", "write_file": "✍️", "search_files": "🔎",
    "discord": "⚙️", "discord_admin": "⚙️", "process_manage": "⚙️",
    "delegate_task": "🔀", "memory": "🧠", "web_search": "🌐", "web_extract": "🌐",
    "browser_navigate": "🌐", "vision_analyze": "🖼️", "todo_list": "📋",
    "cronjob_manage": "⏰", "skill_manage": "📖", "skill_view": "📖",
    "skills_list": "📖", "clarify": "❓", "code_execution": "🐍",
}


def _tool_call_line(name: str, arguments: str) -> str:
    """Approximate the gateway's Discord tool line for one tool call."""
    emoji = _TOOL_EMOJIS.get(name, "⚙️")
    head = f"{emoji} {name}"
    try:
        args = json.loads(arguments or "{}")
    except Exception:
        args = {}
    if not isinstance(args, dict) or not args:
        return head + "..."
    if set(args.keys()) == {"command"} and isinstance(args.get("command"), str):
        cmd = args["command"]
        if len(cmd) > 500:
            cmd = cmd[:497] + "..."
        return f"{head}\n```\n{cmd}\n```"
    keys = list(args.keys())
    args_str = json.dumps(args, ensure_ascii=False, default=str)
    if len(args_str) > 40:
        args_str = args_str[:37] + "..."
    return f"{head}({keys})\n{args_str}"


def _reason_block(reasoning: str) -> str:
    """Render reasoning in the gateway's Discord subtext style."""
    r = (reasoning or "").strip()
    if not r:
        return ""
    lines = "\n".join("-# " + ln if ln.strip() else "-#" for ln in r.split("\n"))
    return f"-# 💭 Reasoning\n{lines}\n\n"


def _render_card(snap: dict) -> str:
    """Mirror of hooks/stats-card/handler.py render()."""
    parts = []
    tps = snap.get("last_call_gen_tok_s") or snap.get("last_call_eff_tok_s")
    if tps is not None:
        parts.append(f"⚡ {tps:g} tok/s")
    ttfb = snap.get("last_call_ttfb_s")
    if ttfb is not None:
        parts.append(f"ttfb {ttfb:g}s")
    dur = snap.get("turn_duration_s")
    if dur is not None:
        parts.append(f"{dur:g}s")

    def fmt(n):
        try:
            return f"{int(n):,}"
        except Exception:
            return "0"

    parts.append(f"{fmt(snap.get('out_tokens'))} out / {fmt(snap.get('in_tokens'))} in")
    cache = snap.get("cache_hit_pct")
    if cache:
        parts.append(f"{cache}% cache")
    calls = int(snap.get("calls") or 1)
    if calls > 1:
        parts.append(f"{calls} calls")
        avg_in = snap.get("avg_in_per_call")
        if avg_in is not None:
            parts.append(f"avg {fmt(round(avg_in))} in/call")
    cost = snap.get("cost_usd")
    if cost is not None:
        parts.append(f"${cost:.4f}")
    return " · ".join(parts)


def _read_card_state(prof):
    try:
        p = os.path.join(prof["stats"], "card_state.json")
        with open(p) as fh:
            return json.load(fh)
    except Exception:
        return None


def _post_chunked(channel_id: str, text: str, token: str) -> list:
    ids = []
    chunks = _chunks(text)
    n = len(chunks)
    for i, chunk in enumerate(chunks, 1):
        if not chunk.strip():
            continue
        body = chunk if n == 1 else f"{chunk} ({i}/{n})"
        resp = _post_discord_message(channel_id, body, token)
        ids.append(resp.get("id"))
    return ids


def _mirror_turn_once(session_id: str, since: float, dry: bool = False):
    """Relay a completed turn natively: tool lines, reasoning+reply, stats card."""
    prof, sess = find_session(session_id)
    if prof is None or sess is None:
        return {"ok": False, "error": "unknown session"}
    if sess["source"] != "discord" or not sess["thread_id"]:
        return {"ok": False, "error": "no discord thread for session"}
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        rows = con.execute(
            "SELECT id, role, content, tool_calls, tool_name, reasoning, timestamp "
            "FROM messages WHERE session_id=? AND timestamp >= ? ORDER BY id",
            (session_id, since - 2),
        ).fetchall()
    finally:
        con.close()

    # Turn boundary: the last user echo at/after `since`; everything after it.
    user_rows = [r for r in rows if r["role"] == "user"]
    if not user_rows:
        return {"ok": False, "error": "no user message found after `since`"}
    turn_start_id = user_rows[-1]["id"]
    turn = [r for r in rows if r["id"] > turn_start_id]

    assistants = [r for r in turn if r["role"] == "assistant"]
    if not assistants:
        return {"ok": False, "error": "no assistant messages yet"}
    last_asst_id = assistants[-1]["id"]

    posts = []
    for r in assistants:
        content = r["content"] or ""
        try:
            tool_calls = json.loads(r["tool_calls"]) if r["tool_calls"] else []
        except Exception:
            tool_calls = []
        if r["id"] == last_asst_id:
            text = _reason_block(r["reasoning"]) + content
            if text.strip():
                posts.append(text)
        else:
            if content.strip():
                posts.append(content)
        for tc in tool_calls or []:
            fn = (tc.get("function") or {}) if isinstance(tc, dict) else {}
            line = _tool_call_line(str(fn.get("name") or "?"), str(fn.get("arguments") or ""))
            if line:
                posts.append(line)

    # Stats card: read fresh card_state for THIS session (brief wait — the
    # recorder writes it at turn end).
    snap = None
    card_deadline = time.time() + 2.5
    while True:
        cand = _read_card_state(prof)
        if isinstance(cand, dict) and cand.get("session_id") == session_id:
            try:
                if float(cand.get("ts") or 0) >= since - 10:
                    snap = cand
                    break
            except Exception:
                pass
        if time.time() >= card_deadline:
            break
        time.sleep(0.3)
    if snap is not None:
        card = _render_card(snap)
        if card:
            posts.append(card)

    if dry:
        return {"ok": True, "dry": True, "posts": len(posts), "preview": [p[:300] for p in posts]}
    target = sess["thread_id"]
    ids = []
    for post in posts:
        ids.extend(_post_chunked(target, post, prof["token"]))
    return {"ok": True, "posts": len(posts), "chunks": len(ids), "message_ids": ids}


def mirror_turn(session_id: str, since: float, dry: bool = False):
    """Relay a completed turn; retries briefly while the turn's writes land."""
    deadline = time.time() + 4.0
    while True:
        res = _mirror_turn_once(session_id, since, dry)
        pending = res.get("error") in {
            "no assistant messages yet",
            "no user message found after `since`",
        }
        if res.get("ok") or not pending or time.time() >= deadline:
            return res
        time.sleep(0.5)


def _turn_cards(session_id: str):
    """Group metrics.jsonl api_call records into per-turn cards."""
    prof, _ = find_session(session_id)
    if prof is None:
        return {"ok": True, "turns": [], "total": {"turns": 0, "calls": 0, "cost_usd": None}}
    path = os.path.join(prof["stats"], "metrics.jsonl")
    turns = {}
    try:
        with open(path) as fh:
            for line in fh:
                if session_id not in line:
                    continue
                try:
                    r = json.loads(line)
                except Exception:
                    continue
                if r.get("session_id") != session_id or r.get("event") != "api_call":
                    continue
                tid = str(r.get("turn_id") or f"ts:{r.get('ts')}")
                t = turns.get(tid)
                if t is None:
                    t = turns[tid] = {
                        "calls": 0, "out": 0, "in": 0, "cache_read": 0, "dur": 0.0,
                        "cost": 0.0, "last_eff": None, "last_gen": None, "last_ttfb": None,
                        "end": 0.0, "model": None, "provider": None,
                    }
                t["calls"] += 1
                t["out"] += int(r.get("out_tokens") or 0)
                t["in"] += int(r.get("in_tokens") or 0)
                t["cache_read"] += int(r.get("cache_read") or 0)
                t["dur"] += float(r.get("duration_s") or 0)
                if r.get("cost_usd") is not None:
                    t["cost"] += float(r["cost_usd"])
                t["last_eff"] = r.get("eff_tok_s")
                t["last_gen"] = r.get("gen_tok_s")
                t["last_ttfb"] = r.get("ttfb_s")
                t["end"] = max(t["end"], float(r.get("ts") or 0))
                t["model"] = r.get("model") or t["model"]
                t["provider"] = r.get("provider") or t["provider"]
    except OSError:
        return {"ok": True, "turns": [], "total": {"turns": 0, "calls": 0, "cost_usd": None}}

    cards = []
    total = {"turns": 0, "calls": 0, "cost_usd": 0.0, "out_tokens": 0, "in_tokens": 0}
    for tid, t in sorted(turns.items(), key=lambda kv: kv[1]["end"]):
        cache_pct = round(100 * t["cache_read"] / t["in"]) if t["in"] else None
        snap = {
            "v": 1, "session_id": session_id, "turn_id": tid, "ts": t["end"],
            "model": t["model"], "provider": t["provider"],
            "calls": t["calls"], "out_tokens": t["out"], "in_tokens": t["in"],
            "cache_hit_pct": cache_pct,
            "turn_duration_s": round(t["dur"], 2),
            "last_call_eff_tok_s": t["last_eff"],
            "last_call_ttfb_s": t["last_ttfb"],
            "last_call_gen_tok_s": t["last_gen"],
            "cost_usd": round(t["cost"], 6) if t["cost"] else None,
            "avg_in_per_call": round(t["in"] / t["calls"], 1) if t["calls"] else None,
        }
        cards.append({"end_ts": t["end"], "line": _render_card(snap), "snap": snap})
        total["turns"] += 1
        total["calls"] += t["calls"]
        total["cost_usd"] = (total["cost_usd"] or 0) + t["cost"]
        total["out_tokens"] += t["out"]
        total["in_tokens"] += t["in"]
    total["cost_usd"] = round(total["cost_usd"], 6) if total["cost_usd"] else None
    return {"ok": True, "turns": cards, "total": total}


def _fts_query(q: str) -> str:
    import re

    toks = [t for t in re.split(r"\s+", q.strip()) if t]
    if not toks:
        return ""
    quoted = ['"' + t.replace('"', '""') + '"' for t in toks]
    quoted[-1] += "*"
    return " ".join(quoted)


def search_messages(q: str, limit: int = 40):
    """Full-text search across every profile's message store (FTS5, LIKE fallback)."""
    fts = _fts_query(q)
    if not fts:
        return {"ok": True, "results": []}
    results = []
    for prof in PROFILES:
        con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
        con.row_factory = sqlite3.Row
        try:
            try:
                rows = con.execute(
                    "SELECT m.id AS message_id, m.session_id, m.role, m.timestamp, "
                    "snippet(messages_fts, 0, '', '', '…', 14) AS snip, s.title "
                    "FROM messages_fts JOIN messages m ON m.id = messages_fts.rowid "
                    "LEFT JOIN sessions s ON s.id = m.session_id "
                    "WHERE messages_fts MATCH ? ORDER BY rank LIMIT ?",
                    (fts, int(limit)),
                ).fetchall()
            except sqlite3.OperationalError:
                like = f"%{q.strip()}%"
                rows = con.execute(
                    "SELECT id AS message_id, session_id, role, timestamp, "
                    "substr(content, 1, 140) AS snip, (SELECT title FROM sessions s WHERE s.id = messages.session_id) AS title "
                    "FROM messages WHERE content LIKE ? ORDER BY id DESC LIMIT ?",
                    (like, int(limit)),
                ).fetchall()
        except Exception:
            rows = []
        finally:
            con.close()
        for r in rows:
            results.append({
                "message_id": r["message_id"],
                "session_id": r["session_id"],
                "role": r["role"],
                "timestamp": r["timestamp"],
                "snippet": r["snip"],
                "title": r["title"],
                "profile": prof["name"],
            })
    # Rank is per-db and not comparable across profiles; merge by recency.
    results.sort(key=lambda r: -(r.get("timestamp") or 0))
    return {"ok": True, "results": results[: int(limit)]}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.split("?")[0] == "/stats/card":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                sid = (q.get("session_id") or [""])[0]
                since = float((q.get("since") or ["0"])[0] or 0)
                prof, _ = find_session(sid)
                if prof is None:
                    self._json(200, {"ok": False, "error": "unknown session"})
                    return
                deadline = time.time() + 4.0
                while True:
                    snap = _read_card_state(prof)
                    if isinstance(snap, dict) and snap.get("session_id") == sid:
                        try:
                            if float(snap.get("ts") or 0) >= since - 10:
                                self._json(200, {"ok": True, "card": _render_card(snap), "snap": snap})
                                return
                        except Exception:
                            pass
                    if time.time() >= deadline:
                        self._json(200, {"ok": False, "error": "no fresh card"})
                        return
                    time.sleep(0.4)
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path == "/health":
            self._json(200, {"ok": True, "service": "atlas-hub",
                             "profiles": [p["name"] for p in PROFILES]})
            return
        if self.path.split("?")[0] == "/stats/turns":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                sid = (q.get("session_id") or [""])[0]
                self._json(200, _turn_cards(sid))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/search":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                query = (q.get("q") or [""])[0]
                limit = int((q.get("limit") or ["40"])[0] or 40)
                self._json(200, search_messages(query, limit))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/spawned":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                self._json(200, spawn_items())
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/spawn-log":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                sid = (q.get("id") or [""])[0]
                kind = (q.get("kind") or [""])[0]
                task = int((q.get("task") or ["0"])[0] or 0)
                lines = int((q.get("lines") or ["400"])[0] or 400)
                text = spawn_log(kind, sid, task, lines)
                if text is None:
                    self._json(404, {"error": "no log"})
                    return
                self._json(200, {"text": text})
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/route":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                self._json(200, session_route(
                    (q.get("profile") or ["default"])[0],
                    (q.get("session") or [""])[0],
                ))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/channels":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                self._json(200, _store_payload((q.get("profile") or ["default"])[0]))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/tree":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                inc = (q.get("include_hidden") or ["0"])[0] == "1"
                self._json(200, build_tree(inc))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        path = self.path.split("?")[0]
        if path.startswith("/channels/"):
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                self._json(200, channels_mutate(path.rsplit("/", 1)[-1], body))
            except ValueError as e:
                self._json(400, {"error": str(e)})
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if path == "/spawned/delete":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                res = spawn_delete(
                    str(body.get("kind") or ""),
                    str(body.get("id") or ""),
                    str(body.get("profile") or ""),
                )
                self._json(200, res)
            except ValueError as e:
                self._json(400, {"error": str(e)})
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if path in ("/mirror", "/mirror_turn"):
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                if path == "/mirror":
                    res = mirror_message(
                        str(body.get("session_id") or ""),
                        str(body.get("role") or ""),
                        str(body.get("content") or ""),
                    )
                else:
                    res = mirror_turn(
                        str(body.get("session_id") or ""),
                        float(body.get("since") or 0),
                        bool(body.get("dry") or False),
                    )
                self._json(200, res)
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        self._json(404, {"error": "not found"})

    def _json(self, code: int, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("atlas-hub: " + fmt % args + "\n")


if __name__ == "__main__":
    if not TOKEN:
        print("atlas-hub: WARNING: no DISCORD_BOT_TOKEN found", file=sys.stderr)
    plist = ", ".join(f"{p['name']}({p['display']})" for p in PROFILES) or "none"
    print(f"atlas-hub on 127.0.0.1:{PORT} — profiles: {plist}", file=sys.stderr, flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
