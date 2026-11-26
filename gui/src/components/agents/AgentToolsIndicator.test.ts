import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentToolsIndicator from './AgentToolsIndicator.vue'

// The hover card opens only on pointer hover: render its parts in place so
// the content can be checked.
const slot = { template: '<div><slot /></div>' }
const stubs = { HoverCard: slot, HoverCardTrigger: slot, HoverCardContent: slot }

describe('AgentToolsIndicator', () => {
  it('links each version to its release', () => {
    const wrapper = mount(AgentToolsIndicator, {
      props: { resticVersion: '0.19.1', rcloneVersion: '1.76.0' },
      global: { stubs },
    })
    const links = wrapper.findAll('a').map((a) => [a.text(), a.attributes('href')])
    expect(links).toEqual([
      ['0.19.1', 'https://github.com/restic/restic/releases/tag/v0.19.1'],
      ['1.76.0', 'https://github.com/rclone/rclone/releases/tag/v1.76.0'],
    ])
  })

  it('renders nothing when the agent reports no versions', () => {
    const wrapper = mount(AgentToolsIndicator, {
      props: { resticVersion: '', rcloneVersion: '' },
      global: { stubs },
    })
    expect(wrapper.find('[data-testid="agent-tools-indicator"]').exists()).toBe(false)
  })
})
