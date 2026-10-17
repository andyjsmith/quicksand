# Authentication

Quicksand has two ways to sign in:

- **Passwords**: Quicksand's own users and passwords. This is the default
  and needs no setup beyond creating users.
- **Single sign-on (SSO)**: through an OpenID Connect provider such as
  Keycloak. Users sign in with their existing account, are created in
  Quicksand on their first sign-in, and can get the administrator role from
  a group at the provider.

The two work side by side: with SSO configured, the sign-in page offers
both, unless you turn passwords off (see [Single sign-on only](#single-sign-on-only)).
Without an `oidc` section in the configuration, Quicksand uses passwords
only, exactly as before.

Either way, signing in creates a Quicksand session (a cookie, valid for
7 days), and everything after that works the same for every user.

## Passwords

### Managing users

Create the first administrator on the Quicksand host:

```sh
sudo -u quicksand quicksand user add -admin <username>
```

After that, administrators add users, reset passwords and grant the
administrator role on the **Admin** page. The same can be done on the host:

| Command | |
| --- | --- |
| `quicksand user add [-admin] <username>` | Create a user (asks for the password) |
| `quicksand user passwd <username>` | Set a user's password and sign them out everywhere |
| `quicksand user admin [-revoke] <username>` | Grant (or with `-revoke`, remove) the administrator role |
| `quicksand user delete <username>` | Delete a user who owns no VMs |
| `quicksand user list` | List users, their role and how they sign in |

The commands use `/etc/quicksand/config.json` (or `config.json` in the
current directory, or `-config`), and run as the `quicksand` user so the
database stays readable by the service.

Usernames are 1–64 letters, digits, `.`, `_`, `-`, `@` or `+`. Passwords
are at least 8 characters and are stored as PBKDF2-SHA256 hashes. Users
change their own password from the menu under their name. Changing or
resetting a password signs that user out everywhere.

### The command line

`qs login https://quicksand.example` asks for a username and password. See
[cli.md](cli.md#signing-in).

## Single sign-on (OpenID Connect)

### How it works

1. **Sign-in:** the sign-in page shows a **Sign in with …** button. Quicksand
   sends the browser to the provider using the authorization code flow with
   PKCE. When the provider sends the user back, Quicksand exchanges the code,
   checks the ID token's signature, audience and nonce, and starts a session.
2. **Who the user is:** users are recognized by the ID token's subject
   (`sub`), which never changes, and named by its `preferred_username` claim
   (configurable). A user renamed at the provider is renamed in Quicksand at
   their next sign-in, and keeps their VMs.
3. **New users:** a user who isn't in Quicksand yet is created on their first
   sign-in, without a password. If a password user already has that name,
   the sign-in is refused, unless you allow linking (see
   [Moving password users to single sign-on](#moving-password-users-to-single-sign-on)).
4. **Administrators:** with `adminGroups`, a user's groups at the provider
   decide whether they are an administrator, checked at each sign-in.
   Without it, you choose administrators on the Admin page as for password
   users.
5. **Signing out:** signing out of Quicksand also ends the session at the
   provider, which then returns the browser to Quicksand's sign-in page. The
   next sign-in asks for credentials again.

Any OpenID Connect provider works. The steps below are for Keycloak; the
[other providers](#other-providers) section has notes for the rest.

### Setting up Keycloak

These steps are for Keycloak 26's admin console, in the realm your users
are in. `https://quicksand.example` stands for Quicksand's address as users
reach it.

**1. Create the client.** Go to **Clients**, then **Create client**:

- **Client type:** OpenID Connect. **Client ID:** `quicksand`.
- **Capability config:** turn **Client authentication** on and keep
  **Standard flow**. Quicksand doesn't use the other flows.
- **Login settings:**
  - **Valid redirect URIs:** `https://quicksand.example/api/auth/oidc/callback`
  - **Valid post logout redirect URIs:** `https://quicksand.example/login`

Save, then copy the secret from the client's **Credentials** tab.
Optionally, under **Advanced**, set **Proof Key for Code Exchange Code
Challenge Method** to `S256` so Keycloak requires the PKCE that Quicksand
always uses.

**2. Send groups in the ID token.** This is needed for `adminGroups` and
`allowedGroups`. On the client's **Client scopes** tab, open
`quicksand-dedicated`, then **Configure a new mapper** (or **Add mapper**,
then **By configuration**), and pick **Group Membership**:

- **Name** and **Token Claim Name:** `groups`
- **Full group path:** either works. Quicksand ignores the leading `/`, so
  `/quicksand-admins` matches `quicksand-admins`. For subgroups with the
  full path on, use the path without the leading slash, e.g.
  `lab/quicksand-admins`.
- **Add to ID token:** on.

**3. Create the groups.** Under **Groups**, create for example
`quicksand-admins`, and `quicksand-users` if you want to restrict who can
sign in. Add users to them under each user's **Groups** tab.

### Configuring Quicksand

Add an `oidc` section to `/etc/quicksand/config.json` and restart Quicksand:

```json
"oidc": {
  "issuer": "https://keycloak.example/realms/lab",
  "clientId": "quicksand",
  "clientSecret": "<the client's secret>",
  "redirectURL": "https://quicksand.example/api/auth/oidc/callback",
  "name": "Keycloak",
  "adminGroups": ["quicksand-admins"]
}
```

| Setting | Default | |
| --- | --- | --- |
| `issuer` | (required) | The provider's issuer URL. For Keycloak, the realm's URL: `https://<keycloak>/realms/<realm>`. |
| `clientId` | (required) | The client's ID. |
| `clientSecret` | | The client's secret. |
| `redirectURL` | (required) | Quicksand's callback, exactly as registered: `https://<quicksand>/api/auth/oidc/callback`. Signing out returns to `/login` on the same address. |
| `name` | `Single sign-on` | Label of the sign-in button ("Sign in with Keycloak"). |
| `scopes` | `["profile", "email"]` | Scopes requested besides `openid`. |
| `usernameClaim` | `preferred_username` | ID token claim users are named by, e.g. `email`. |
| `groupsClaim` | `groups` | ID token claim listing the user's groups. |
| `adminGroups` | | Members of any of these groups are administrators. Other SSO users are not. |
| `allowedGroups` | | If set, only members of these groups (or of `adminGroups`) can sign in. |
| `linkLocalUsers` | `false` | Let a first SSO sign-in take over the password user of the same name. |
| `disablePasswords` | `false` | Turn password sign-in off. |

Quicksand fetches the provider's configuration when it starts. If the
provider can't be reached then, it logs a warning and tries again at the
next sign-in, so Quicksand starts either way. The Quicksand host must be
able to reach the provider over HTTPS with a certificate the system
trusts. For an internal CA, add it to the host's trust store
(`/usr/local/share/ca-certificates` and `update-ca-certificates` on
Debian).

The configuration holds the client secret, so keep it readable only by
the service (`chmod 0640`, group `quicksand`, as the installer sets it).
Serve Quicksand over HTTPS with `"secureCookies": true`; sessions are
bearer cookies whether they came from a password or SSO.

### Administrators

With `adminGroups` set, the administrator role of SSO users follows their
groups:

- **Who decides:** at each sign-in, members of any listed group become
  administrators and everyone else loses the role. A change at the provider
  applies at the user's next sign-in. Sign the user out (or wait for their
  session to end) to apply it sooner.
- **The Admin page:** for SSO users, the role can't be changed there; the
  menu items are disabled. Password users are unaffected and still managed
  on the Admin page.
- **On the host:** `quicksand user admin` still works, but the user's groups
  decide again at their next sign-in.

Without `adminGroups`, SSO users start as regular users and you grant the
role on the Admin page. If SSO is the only way in, make the first
administrator on the host after they have signed in once:

```sh
sudo -u quicksand quicksand user admin alice
```

### Restricting who can sign in

With `allowedGroups`, only members of those groups or of `adminGroups` can
sign in. Others see "your account isn't in a group allowed to use
Quicksand". Existing users removed from the groups can't sign in again, but
keep any session they already have until it ends; delete them on the Admin
page to end it at once (once they own no VMs). You can also restrict access
in Keycloak itself, for example by requiring a role for the client.

### Moving password users to single sign-on

By default, if an SSO user's name matches an existing password user, the
sign-in is refused. This stops someone who can choose their username at the
provider from taking over an account.

If the provider controls usernames (as Keycloak does unless users may edit
theirs), set `"linkLocalUsers": true`. Each password user then becomes an
SSO user at their first SSO sign-in, keeping their VMs and their password.
You can turn the option off again once everyone has moved.

SSO users have no password, so they can't use password sign-in. An
administrator can give one a password (**Set password** on the Admin page),
for example for scripts that use `qs login --password`.

### Single sign-on only

With `"disablePasswords": true`:

- the sign-in page shows only the SSO button;
- password sign-in is refused, also for `qs login --password`;
- the Admin page no longer adds users or sets passwords, and users can't
  change theirs.

Keep a way back in case the provider is down. Setting `disablePasswords`
to `false` (and restarting) brings password sign-in back for any user who
has a password.

### The command line

When the server has SSO, `qs login https://quicksand.example` signs in
through the browser:

1. `qs` opens your browser to sign in (and prints the URL in case it can't).
2. After you sign in, Quicksand sends the browser to a temporary listener of
   `qs` on `127.0.0.1` with a one-time code.
3. `qs` trades the code, together with a secret that never left `qs`, for a
   session. A code caught by anyone else is useless without that secret.

The browser must run on the same machine as `qs`. Over SSH, either sign in
with a password (`qs login --password`, if you have one), or forward the
port `qs` prints in the URL (`cli_port`) with `ssh -L`. `qs login --password`
also skips the browser when the server offers both. See
[cli.md](cli.md#signing-in).

### Other providers

Any provider with OpenID Connect discovery works. Register a confidential
web client with the redirect URI above, then:

- **Usernames:** set `usernameClaim` to a claim that names users. Many
  providers send `preferred_username`. Others are better served by `email`
  (Google, for example). Usernames must be valid Quicksand usernames (see
  above).
- **Groups:** set `groupsClaim` to the claim the provider puts groups in,
  and make sure it is in the ID token. Some providers send group IDs rather
  than names (Microsoft Entra ID, for example); list those IDs in
  `adminGroups` and `allowedGroups`.
- **Signing out:** providers without an `end_session_endpoint` only sign
  the user out of Quicksand.

### Troubleshooting

Sign-in errors are shown on the sign-in page (or by `qs`) and logged by
Quicksand (`journalctl -u quicksand`).

| Message | Cause |
| --- | --- |
| can't reach the identity provider | Wrong `issuer`, or the host can't reach it or doesn't trust its certificate. |
| the identity provider refused the sign-in | Usually a wrong `clientSecret` or `redirectURL`. Keycloak's events (Realm settings, then Events) give details. |
| the identity provider sent no "preferred_username" claim | Add the `profile` scope, or set `usernameClaim` to a claim the provider sends. |
| a Quicksand user named … already exists | A password user has that name; see [Moving password users to single sign-on](#moving-password-users-to-single-sign-on). |
| your account isn't in a group allowed to use Quicksand | Not in `allowedGroups` or `adminGroups`, or the groups claim isn't in the ID token. |
| this sign-in wasn't started in this browser, or took too long | The sign-in took over 10 minutes, Quicksand restarted in between, or the redirect landed on a different address than the one the sign-in started on (check `redirectURL`). |
| Keycloak shows "Invalid parameter: redirect_uri" | `redirectURL` doesn't match the client's Valid redirect URIs. |
| Keycloak shows a sign-out confirmation, or an error, after signing out | Add `https://<quicksand>/login` to the client's Valid post logout redirect URIs. |

To see the claims Keycloak sends, use the client's **Client scopes** tab,
then **Evaluate**, then **Generated ID token**, for a given user.
