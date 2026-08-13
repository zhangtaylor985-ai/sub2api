import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../SessionDeliveryView.vue')
const source = readFileSync(componentPath, 'utf8')

describe('Session delivery console contract', () => {
  it('polls without overlap and cancels requests during teardown', () => {
    expect(source).toContain('if (loading.value) return')
    expect(source).toContain('window.setInterval(refreshAll, 15_000)')
    expect(source).toContain('refreshController?.abort()')
    expect(source).toContain('policyController?.abort()')
  })

  it('exposes all capture modes and the transactional only-key action', () => {
    expect(source).toContain("value: 'all' as const")
    expect(source).toContain("value: 'selected' as const")
    expect(source).toContain("value: 'disabled' as const")
    expect(source).toContain('sessionDeliveryAPI.setOnlyAPIKey(action.key.id)')
    expect(source).toContain('<ConfirmDialog')
  })

  it('renders only the configured public delivery model', () => {
    expect(source).toContain('overview.value?.public_model')
    expect(source).not.toMatch(/gpt-5\.[0-9]/i)
  })
})
