export class SpiceMiniData {
  build_msg(type: number, extra: unknown): void
}
export class SpiceMsgcKeyDown {
  code: number
}
export class SpiceMsgcKeyUp {
  code: number
}
export class SpiceMsgClipboardSend {
  constructor(type: number, text: string, caps: number[])
}
export class SpiceMsgClipboardReceive {
  constructor(agentData: unknown, caps: number[])
  type: number
  get_text(): string
}
