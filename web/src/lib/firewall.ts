// A VM's firewall as users see it: per network (Egress, the user network;
// Link, the link network), a default for each direction and an ordered list
// of rules. The first matching rule decides; the default covers the rest.
// Replies to allowed connections always get through (the firewall is
// stateful), and Quicksand's own connection to its agent is never affected.

export type FirewallNetwork = 'egress' | 'link'
export type Direction = 'out' | 'in'
export type FirewallAction = 'allow' | 'block'
export type Protocol = 'any' | 'tcp' | 'udp' | 'icmp'

export interface FirewallRule {
  id: string
  enabled: boolean
  direction: Direction
  action: FirewallAction
  protocol: Protocol
  /** "any", "private" (RFC 1918 and friends), or addresses/CIDRs separated by commas. */
  remote: string
  /** Ports for TCP/UDP: "443", "8000-8080", "80,443"; empty for all. */
  ports: string
  comment: string
}

export interface NetworkFirewall {
  outbound: FirewallAction
  inbound: FirewallAction
  rules: FirewallRule[]
}

export interface VMFirewall {
  egress: NetworkFirewall
  link: NetworkFirewall
}

export const networkLabels: Record<FirewallNetwork, string> = { egress: 'Egress', link: 'Link network' }

export const emptyNetwork = (): NetworkFirewall => ({ outbound: 'allow', inbound: 'allow', rules: [] })

let nextId = 0
export const newRuleId = () => `new-${Date.now().toString(36)}-${nextId++}`

export function newRule(partial: Partial<FirewallRule> = {}): FirewallRule {
  return {
    id: newRuleId(),
    enabled: true,
    direction: 'out',
    action: 'block',
    protocol: 'any',
    remote: 'any',
    ports: '',
    comment: '',
    ...partial,
  }
}

/** The private ranges "private" stands for. */
export const privateRanges = ['10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '169.254.0.0/16', 'fc00::/7', 'fe80::/10']

export interface Preset {
  id: string
  label: string
  description: string
  networks: FirewallNetwork[]
  apply(): NetworkFirewall
}

export const presets: Preset[] = [
  {
    id: 'internet-only',
    label: 'Internet only',
    description: 'Blocks the local network (private addresses), except DNS so names still resolve.',
    networks: ['egress'],
    apply: () => ({
      outbound: 'allow',
      inbound: 'allow',
      rules: [
        newRule({ action: 'allow', protocol: 'udp', ports: '53', comment: 'DNS' }),
        newRule({ action: 'allow', protocol: 'tcp', ports: '53', comment: 'DNS' }),
        newRule({ action: 'block', remote: 'private', comment: 'Local network' }),
      ],
    }),
  },
  {
    id: 'no-inbound',
    label: 'Block all inbound',
    description: 'Nothing can connect to the VM; it can still connect out.',
    networks: ['egress', 'link'],
    apply: () => ({ outbound: 'allow', inbound: 'block', rules: [] }),
  },
  {
    id: 'isolated',
    label: 'Cut off',
    description: 'Blocks all traffic in both directions.',
    networks: ['egress', 'link'],
    apply: () => ({ outbound: 'block', inbound: 'block', rules: [] }),
  },
  {
    id: 'web-only',
    label: 'Web browsing only',
    description: 'Allows DNS and web traffic (HTTP, HTTPS) out; blocks everything else.',
    networks: ['egress'],
    apply: () => ({
      outbound: 'block',
      inbound: 'block',
      rules: [
        newRule({ action: 'allow', protocol: 'udp', ports: '53', comment: 'DNS' }),
        newRule({ action: 'allow', protocol: 'tcp', ports: '53', comment: 'DNS' }),
        newRule({ action: 'allow', protocol: 'tcp', ports: '80,443', comment: 'Web' }),
        newRule({ action: 'allow', protocol: 'udp', ports: '443', comment: 'Web (QUIC)' }),
      ],
    }),
  },
  {
    id: 'open',
    label: 'Allow everything',
    description: 'No filtering: the default.',
    networks: ['egress', 'link'],
    apply: emptyNetwork,
  },
]

export const remoteLabel = (remote: string) =>
  remote === 'any' ? 'Anywhere' : remote === 'private' ? 'Private networks' : remote

export const protocolLabel = (p: Protocol) => (p === 'any' ? 'Any protocol' : p.toUpperCase())

/** A rule in words, e.g. "Block outbound TCP to private networks on port 22". */
export function describeRule(r: FirewallRule): string {
  const verb = r.action === 'allow' ? 'Allow' : 'Block'
  const dir = r.direction === 'out' ? 'outbound' : 'inbound'
  const proto = r.protocol === 'any' ? '' : ` ${r.protocol.toUpperCase()}`
  const where = r.remote === 'any' ? '' : ` ${r.direction === 'out' ? 'to' : 'from'} ${remoteLabel(r.remote).toLowerCase()}`
  const ports =
    r.ports && (r.protocol === 'tcp' || r.protocol === 'udp')
      ? ` on port${/[,-]/.test(r.ports) ? 's' : ''} ${r.ports}`
      : ''
  return `${verb} ${dir}${proto}${where}${ports}`
}

const cidrRe = /^(\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?|[0-9a-f:]+:[0-9a-f:]*(\/\d{1,3})?)$/i
const portsRe = /^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$/

/** Problems with a rule, by field. */
export function validateRule(r: FirewallRule): Partial<Record<'remote' | 'ports', string>> {
  const errors: Partial<Record<'remote' | 'ports', string>> = {}
  if (r.remote !== 'any' && r.remote !== 'private') {
    const parts = r.remote.split(',').map((p) => p.trim()).filter(Boolean)
    if (!parts.length || parts.some((p) => !cidrRe.test(p))) {
      errors.remote = 'Enter IP addresses or ranges (CIDR), separated by commas, e.g. 203.0.113.5, 10.1.0.0/16'
    }
  }
  if (r.ports && (r.protocol === 'tcp' || r.protocol === 'udp')) {
    const p = r.ports.replace(/\s/g, '')
    const bad = !portsRe.test(p) || p.split(/[,-]/).some((n) => +n < 1 || +n > 65535)
    if (bad) errors.ports = 'Ports are 1-65535: a port, a range like 8000-8080, or a list like 80,443'
  }
  return errors
}
