<script setup lang="ts">
import { Play } from '@lucide/vue'
import { ref } from 'vue'
import { api, decodeOutput, errorMessage, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

const props = defineProps<{ vm: VM }>()

const command = ref('')
const running = ref(false)
const result = ref<{ command: string; stdout: string; stderr: string; status: string; failed: boolean } | null>(null)

async function run() {
  const cmd = command.value.trim()
  if (!cmd) return
  running.value = true
  try {
    const res = await api.exec(props.vm.id, { shell: cmd, timeoutSeconds: 60 })
    const status = res.error
      ? res.error
      : res.timedOut
        ? 'timed out after 60 s'
        : `exit ${res.exitCode} · ${res.durationMs} ms${res.truncated ? ' · output truncated' : ''}`
    result.value = {
      command: cmd,
      stdout: decodeOutput(res.stdout),
      stderr: decodeOutput(res.stderr),
      status,
      failed: !!res.error || !!res.timedOut || res.exitCode !== 0,
    }
  } catch (e) {
    result.value = { command: cmd, stdout: '', stderr: '', status: errorMessage(e), failed: true }
  } finally {
    running.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-2">
    <p class="text-muted-foreground flex items-center gap-1.5 text-xs">
      <span
        class="inline-block size-2 rounded-full"
        :class="vm.agent?.status === 'connected' ? 'bg-emerald-500' : 'bg-muted-foreground'"
      />
      <template v-if="vm.agent?.status === 'connected'">
        Connected to {{ vm.agent.hostname }} ({{ vm.agent.os }}) at {{ vm.agent.ip }}
      </template>
      <template v-else>Not reachable at {{ vm.agent?.ip }} yet</template>
    </p>
    <form class="flex gap-1.5" @submit.prevent="run">
      <Input
        v-model="command"
        :placeholder="vm.agent?.os === 'windows' ? 'ipconfig /all' : 'uname -a'"
        class="font-mono text-xs"
        aria-label="Command to run in the guest"
      />
      <Button type="submit" size="icon" :disabled="running || !command.trim()" title="Run">
        <Spinner v-if="running" />
        <Play v-else />
      </Button>
    </form>
    <div v-if="result" class="flex flex-col gap-1">
      <p class="text-xs" :class="result.failed ? 'text-destructive' : 'text-muted-foreground'">
        <span class="font-mono">{{ result.command }}</span> — {{ result.status }}
      </p>
      <pre
        v-if="result.stdout || result.stderr"
        class="bg-muted max-h-64 overflow-auto rounded-md p-2 font-mono text-[0.7rem] leading-snug whitespace-pre-wrap"
      >{{ result.stdout }}<span v-if="result.stderr" class="text-destructive">{{ result.stderr }}</span></pre>
    </div>
  </div>
</template>
