# Open in Quicksand browser extension

The extension adds **Open in Quicksand** to the right-click menu of links in
Chrome (and other Chromium browsers, such as Edge and Brave) and Firefox. It
opens the link in a new VM of an [app template](app-templates.md) that opens
links, such as Chrome or Firefox in a throwaway VM, or in a new tab of one
you already have running, so the page never runs on your own computer.

## Installing

Each release has `quicksand-extension-chrome.zip` and
`quicksand-extension-firefox.zip`. To build them yourself, run
`./scripts/build-extension.sh`, which writes them (and unpacked copies) to
`dist/extension/`.

- **Chrome, Edge, Brave:** unzip it, open `chrome://extensions`, turn on
  **Developer mode**, click **Load unpacked** and choose the folder.
  Organizations can also publish it to their own Chrome Web Store listing or
  install it by policy.
- **Firefox:** Firefox only installs signed add-ons permanently. Sign the zip
  through [addons.mozilla.org](https://addons.mozilla.org/developers/) (it
  can be signed for self-distribution, without being listed), then open the
  signed `.xpi`. To try it unsigned, open `about:debugging#/runtime/this-firefox`
  and use **Load Temporary Add-on** with the zip; it lasts until Firefox
  restarts. Firefox 128 or newer is needed.

## Setting it up

Open the extension's options (in Chrome, the Extensions menu in the
toolbar, then the extension's ⋮ menu and **Options**; in Firefox,
`about:addons`, then the extension's **Preferences**) and enter:

- **Quicksand address**, e.g. `https://quicksand.example`.
- **Template ID**: the template links open in, which must open links. IDs
  are shown next to template names on Quicksand's dashboard, with
  "Opens links" on those that can.
- **Network ID** (optional): the network chosen to start with. Without it,
  it's Quicksand's default network. Either way, it can be changed before
  opening each link.

Using **Open in Quicksand** before setting it up opens the options instead.

## How it works

The extension doesn't talk to Quicksand or hold any credentials. Clicking
**Open in Quicksand** opens Quicksand's `/open` page for the link in a new
tab:

```
https://quicksand.example/open?template=chrome&url=https%3A%2F%2Fexample.com%2F&network=default
```

That page runs as you, in your normal Quicksand session; if you aren't
signed in, Quicksand has you sign in first and then carries on. It shows the
link and, by default, deploys a **New VM** of the template on the network
shown (which you can change); choose **Existing VM** to open it in a new tab
of one of your running VMs that open links instead. Nothing happens until
you click **Open** (or press Enter). Then it shows the VM's console, which
for a new VM follows the deployment and connects when it's up. It asks first
because any website could send you to that page, and shouldn't be able to
deploy VMs as you.

The menu item only appears on `http` and `https` links, the only kind
Quicksand opens.

Permissions: `contextMenus` for the menu item, and `storage` for the
options.
