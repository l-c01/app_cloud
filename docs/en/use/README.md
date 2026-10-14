# Using it

For people who want to use the client.

> **Status: this build has never been run on real hardware. Do not use it
> for anything you depend on.**
> See "What this build can do" at the end.

---

## 1. What it is

A proxy client for Xboard panels. It is a **client**: it provides no proxy
service of its own. You need your own panel account or subscription link.

What sets it apart:

- **One core (sing-box)** — the core ships with the application, so you do not
  install it separately.
- **Two interface modes** — simple mode has just connect/disconnect;
  professional mode exposes everything. Switching modes **does not**
  change behaviour that is already established.
- **The interface follows the system language** and can be overridden.

---

## 2. First run

### 2.1 Getting a subscription link

Either route works:

**A. Through a panel (needs an account)**

1. Open Settings and enter the panel address.
2. Sign in with your email and password.
3. The subscription link is fetched automatically.

The panel address is yours to provide. The software **ships with no panel
address at all**. That is not only a privacy choice: a preset address would
point everybody's traffic at the same place.

**B. Paste an existing link**

If you already have a subscription link, paste it directly.

> **A subscription link is a credential.** Anyone who has it can use your
> account's traffic. Do not post it in group chats, screenshots or
> untrusted websites. This client replaces it with `<redacted>` in logs
> and diagnostics.

### 2.2 Choosing how traffic is captured

| Mode | When to use | Notes |
|---|---|---|
| **System proxy** (desktop default) | Most cases | Only affects applications that read the system proxy setting |
| **TUN interface** | When all traffic must be captured | Usually needs administrator rights |
| **PAC** | Rules decide what goes through the proxy | Rules are served locally |
| **Local ports only** | You configure other software yourself | Changes no system settings |
| **VPN** (Android) | The only option on Android | Android asks for consent |

**A system proxy is not all traffic.** Applications that ignore the system
proxy setting will not use it — they may still connect directly. That is
not a bug; it is the inherent limit of this kind of capture. Only TUN or
VPN capture everything.

---

## 3. The interface

### Simple mode

Three things: the current state, the current node, and the connect button.

It deliberately **omits** routing policy, traffic entry and
logs. These are not hidden "advanced" features; they are choices that let
people get it wrong. A user who does not understand routing rules picks
"direct everything", sees "connected but no web pages open", and has no way
to know why.

### Professional mode

Exposes all three dimensions:

- **Traffic entry**: see the table above
- **Routing policy**: rule based / proxy everything / direct everything
- **Node list**

There is no core to choose: this build ships sing-box only.

---

## 4. When something goes wrong

### Connected, but no web pages open

Check in this order:

1. **Is the traffic entry right?** With "direct everything", all traffic
   goes out directly — not reaching blocked sites is expected.
2. **Is it a system proxy that the application ignores?** Try TUN.
3. **Is the node itself working?** Switch to another one.
4. **Look at the log** (professional mode). Credentials are filtered out
   of it automatically.

### No network at all after disconnecting

This means a system network setting was not restored to your original value.

Professional mode shows a "network needs repair" state. It lists **which
setting** differs and what its current value is. It **will not overwrite
it** — that value is quite likely something you changed yourself, and
overwriting it would erase your edit.

### "The background service is not running"

The desktop interface and the service that does the work are two separate
processes. With the service stopped, the window opens but cannot control
the proxy.

### "Network settings conflict"

A system network setting was changed by hand while the client was running.
The client noticed it differs from what this connection wrote, so it
**left it alone**.

That is deliberate: overwriting would erase your change, and you would not
know who did it.

---

## 5. Privacy

| Item | Handling |
|---|---|
| Subscription links, session credentials | Treated as credentials: never in logs, exports or diagnostics |
| Cloud backup | **Disabled**, with every data domain explicitly excluded |
| Panel address | **None is baked into the source**; you supply it |
| Crash reports | Not sent |
| Core binaries | Downloaded only from official releases, with SHA256 verification |

On Android `allowBackup=false`: when you change devices, sign in again,
rather than letting credentials be copied somewhere you may not expect.

---

## 6. What this build can do

Stated plainly, because this matters more than a feature list:

| Feature | Status |
|---|---|
| Panel sign-in, fetching the subscription link | Code written, **never compiled** |
| Subscription fetch and node parsing | Code written, **never compiled** |
| sing-box config compilation | Code written, **never compiled**; capability table **not validated against pinned cores** |
| Desktop interface (both modes) | Code written, **never compiled** |
| Android interface | Code written, **never compiled** |
| Android traffic capture | **Not implemented**: the APK installs, opens and creates a VPN, but does not proxy traffic |
| In-app core updates | **Not implemented** |
| Runtime verification on real hardware | **Never done at all** |

"Never compiled" means exactly that: no compiler has ever looked at this
code on the development machine. All compilation and testing happens in
GitHub Actions, and that is the only authority.

---

## 7. More

- [Architecture](../dev/architecture.md) — how the pieces fit together
- [Upload and build](../dev/upload-and-build.md) — build your own
- [Verification](../dev/verification.md) — what has and has not been checked
