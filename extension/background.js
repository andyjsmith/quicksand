// Adds "Open in Quicksand" to the menu of links. It opens Quicksand's /open
// page for the link in a new tab, which (once the user is signed in and
// confirms) deploys a VM of the configured template to open the link in.
// The extension never talks to Quicksand itself.

const MENU_ID = 'open-in-quicksand'

function createMenu() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: MENU_ID,
      title: 'Open in Quicksand',
      contexts: ['link'],
      targetUrlPatterns: ['http://*/*', 'https://*/*'],
    })
  })
}

chrome.runtime.onInstalled.addListener(createMenu)
chrome.runtime.onStartup.addListener(createMenu)

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId === MENU_ID && info.linkUrl) openLink(info.linkUrl, tab)
})

// openLink opens Quicksand's /open page for a link, next to tab.
async function openLink(linkUrl, tab) {
  const { baseURL, template, network } = await chrome.storage.sync.get(['baseURL', 'template', 'network'])
  if (!baseURL || !template) {
    chrome.runtime.openOptionsPage()
    return
  }
  const params = new URLSearchParams({ template, url: linkUrl })
  if (network) params.set('network', network)
  const page = new URL('/open', baseURL)
  page.search = params.toString()
  const create = { url: page.href }
  if (tab && tab.id !== undefined && tab.id >= 0) {
    create.index = tab.index + 1
    create.openerTabId = tab.id
  }
  chrome.tabs.create(create)
}
