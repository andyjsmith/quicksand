const form = document.getElementById('form')
const baseURL = document.getElementById('baseURL')
const template = document.getElementById('template')
const network = document.getElementById('network')
const status = document.getElementById('status')

chrome.storage.sync.get(['baseURL', 'template', 'network']).then((saved) => {
  baseURL.value = saved.baseURL || ''
  template.value = saved.template || ''
  network.value = saved.network || ''
})

form.addEventListener('submit', async (e) => {
  e.preventDefault()
  status.className = ''
  let url
  try {
    url = new URL(baseURL.value.trim())
    if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error()
  } catch {
    status.textContent = 'Enter an http:// or https:// address.'
    status.className = 'error'
    return
  }
  // Quicksand is served from the root of its address.
  baseURL.value = url.origin
  template.value = template.value.trim()
  network.value = network.value.trim()
  await chrome.storage.sync.set({ baseURL: url.origin, template: template.value, network: network.value })
  status.textContent = 'Saved.'
})
