import type { Component } from 'vue'
import { Bell, Hash, Link, Smartphone, Users } from '@lucide/vue'

// Notification provider types, mirroring providerTypes in
// server/internal/notification/provider.go: the server builds the shoutrrr
// URL from these fields and is the authority on their validation.

export interface ProviderFieldDef {
  key: string
  label: string
  secret?: boolean
  required?: boolean
  placeholder?: string
  help?: string
}

export interface ProviderTypeDef {
  type: string
  label: string
  // Brand logo path (24×24 viewBox) or, for brands simple-icons does not
  // carry, a generic icon.
  iconPath?: string
  icon?: Component
  fields: ProviderFieldDef[]
  // Whether the service takes a priority, and the values it accepts.
  priorityHelp?: string
}

// Brand logo paths from simple-icons 16.36.0 (https://simpleicons.org, CC0-1.0).
// Gotify, Pushover, Slack and Microsoft Teams are not in simple-icons.
const LOGOS = {
  ntfy: 'M12.597 13.693v2.156h6.205v-2.156ZM5.183 6.549v2.363l3.591 1.901.023.01-.023.009-3.591 1.901v2.35l.386-.211 5.456-2.969V9.729ZM3.659 2.037C1.915 2.037.42 3.41.42 5.154v.002L.438 18.73 0 21.963l5.956-1.583h14.806c1.744 0 3.238-1.374 3.238-3.118V5.154c0-1.744-1.493-3.116-3.237-3.117h-.001zm0 2.2h17.104c.613.001 1.037.447 1.037.917v12.108c0 .47-.424.916-1.038.916H5.633l-3.026.915.031-.179-.017-13.76c0-.47.424-.917 1.038-.917z',
  telegram: 'M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z',
  discord: 'M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189Z',
  matrix: 'M.632.55v22.9H2.28V24H0V0h2.28v.55zm7.043 7.26v1.157h.033c.309-.443.683-.784 1.117-1.024.433-.245.936-.365 1.5-.365.54 0 1.033.107 1.481.314.448.208.785.582 1.02 1.108.254-.374.6-.706 1.034-.992.434-.287.95-.43 1.546-.43.453 0 .872.056 1.26.167.388.11.716.286.993.53.276.245.489.559.646.951.152.392.23.863.23 1.417v5.728h-2.349V11.52c0-.286-.01-.559-.032-.812a1.755 1.755 0 0 0-.18-.66 1.106 1.106 0 0 0-.438-.448c-.194-.11-.457-.166-.785-.166-.332 0-.6.064-.803.189a1.38 1.38 0 0 0-.48.499 1.946 1.946 0 0 0-.231.696 5.56 5.56 0 0 0-.06.785v4.768h-2.35v-4.8c0-.254-.004-.503-.018-.752a2.074 2.074 0 0 0-.143-.688 1.052 1.052 0 0 0-.415-.503c-.194-.125-.476-.19-.854-.19-.111 0-.259.024-.439.074-.18.051-.36.143-.53.282-.171.138-.319.337-.439.595-.12.259-.18.6-.18 1.02v4.966H5.46V7.81zm15.693 15.64V.55H21.72V0H24v24h-2.28v-.55z',
  googlechat: 'M1.637 0C.733 0 0 .733 0 1.637v16.5c0 .904.733 1.636 1.637 1.636h3.955v3.323c0 .804.97 1.207 1.539.638l3.963-3.96h11.27c.903 0 1.636-.733 1.636-1.637V5.592L18.408 0Zm3.955 5.592h12.816v8.59H8.455l-2.863 2.863Z',
  mattermost: 'M12.081 0C7.048-.034 2.339 3.125.637 8.153c-2.125 6.276 1.24 13.086 7.516 15.21 6.276 2.125 13.086-1.24 15.21-7.516 1.727-5.1-.172-10.552-4.311-13.557l.126 2.547c2.065 2.282 2.88 5.512 1.852 8.549-1.534 4.532-6.594 6.915-11.3 5.321-4.708-1.593-7.28-6.559-5.745-11.092 1.031-3.046 3.655-5.121 6.694-5.67l1.642-1.94A4.87 4.87 0 0 0 12.08 0zm3.528 1.094a.284.284 0 0 0-.123.024l-.004.001a.33.33 0 0 0-.109.071c-.145.142-.657.828-.657.828L13.6 3.4l-1.3 1.585-2.232 2.776s-1.024 1.278-.798 2.851c.226 1.574 1.396 2.34 2.304 2.648.907.307 2.302.408 3.438-.704 1.135-1.112 1.098-2.75 1.098-2.75l-.087-3.56-.07-2.05-.047-1.775s.01-.856-.02-1.057a.33.33 0 0 0-.035-.107l-.006-.012-.007-.011a.277.277 0 0 0-.229-.14z',
}

const webhookField = (placeholder: string, help: string): ProviderFieldDef => ({
  key: 'webhook_url', label: 'Webhook URL', secret: true, required: true, placeholder, help,
})

const TYPES: ProviderTypeDef[] = [
  {
    type: 'gotify', label: 'Gotify', icon: Bell,
    fields: [
      { key: 'url', label: 'Server URL', required: true, placeholder: 'https://gotify.example.com',
        help: 'Include the base path if Gotify is behind a reverse proxy. http:// disables TLS.' },
      { key: 'token', label: 'Application token', secret: true, required: true,
        help: 'Create an application in Gotify and copy its token.' },
    ],
    priorityHelp: '0 to 10. 0 is silent on Android, 8 and above pops up with sound and vibration.',
  },
  {
    type: 'ntfy', label: 'ntfy', iconPath: LOGOS.ntfy,
    fields: [
      { key: 'url', label: 'Server URL', required: true, placeholder: 'https://ntfy.sh',
        help: 'https://ntfy.sh for the public server, or your own.' },
      { key: 'topic', label: 'Topic', secret: true, required: true, placeholder: 'arkeep-backups',
        help: 'On a public server, anyone who knows the topic can read it: pick one hard to guess.' },
      { key: 'token', label: 'Access token', secret: true, help: 'Only for a server or topic that requires one.' },
    ],
    priorityHelp: '1 (min) to 5 (max), or min, low, default, high, max.',
  },
  {
    type: 'pushover', label: 'Pushover', icon: Smartphone,
    fields: [
      { key: 'user_key', label: 'User key', secret: true, required: true, help: 'Shown on your Pushover dashboard.' },
      { key: 'api_token', label: 'Application API token', secret: true, required: true },
      { key: 'devices', label: 'Devices', placeholder: 'phone, tablet',
        help: 'Comma-separated. Leave empty to notify all your devices.' },
    ],
    priorityHelp: '-2 (no alert) to 1 (high). 2 (emergency) repeats until acknowledged.',
  },
  {
    type: 'telegram', label: 'Telegram', iconPath: LOGOS.telegram,
    fields: [
      { key: 'bot_token', label: 'Bot token', secret: true, required: true, placeholder: '123456789:ABC…',
        help: 'Create a bot with @BotFather and add it to the chats.' },
      { key: 'chats', label: 'Chat IDs', required: true, placeholder: '123456789, @mychannel',
        help: 'Comma-separated chat IDs or @channel names.' },
    ],
  },
  {
    type: 'discord', label: 'Discord', iconPath: LOGOS.discord,
    fields: [webhookField('https://discord.com/api/webhooks/…', 'Channel settings → Integrations → Webhooks → Copy Webhook URL.')],
  },
  {
    type: 'matrix', label: 'Matrix', iconPath: LOGOS.matrix,
    fields: [
      { key: 'url', label: 'Homeserver URL', required: true, placeholder: 'https://matrix.example.com' },
      { key: 'user', label: 'User', required: true, placeholder: 'arkeep-bot' },
      { key: 'password', label: 'Password', secret: true, required: true },
      { key: 'rooms', label: 'Rooms', placeholder: '!roomid:example.com, #alias:example.com',
        help: 'Comma-separated. Leave empty to post to every room the user has joined.' },
    ],
  },
  {
    type: 'slack', label: 'Slack', icon: Hash,
    fields: [webhookField('https://hooks.slack.com/services/…', 'The URL of an incoming webhook of your Slack app.')],
  },
  {
    type: 'teams', label: 'Microsoft Teams', icon: Users,
    fields: [webhookField('https://….environment.api.powerplatform.com/…',
      'The URL of a Workflows (Power Automate) webhook: "Post to a channel when a webhook request is received".')],
  },
  {
    type: 'googlechat', label: 'Google Chat', iconPath: LOGOS.googlechat,
    fields: [webhookField('https://chat.googleapis.com/v1/spaces/…', 'Space → Apps & integrations → Webhooks.')],
  },
  {
    type: 'mattermost', label: 'Mattermost', iconPath: LOGOS.mattermost,
    fields: [webhookField('https://mattermost.example.com/hooks/…', 'Integrations → Incoming Webhooks.')],
  },
  {
    type: 'custom', label: 'Other (shoutrrr URL)', icon: Link,
    fields: [
      { key: 'url', label: 'Shoutrrr URL', secret: true, required: true, placeholder: 'bark://devicekey@api.day.app',
        help: 'Any service supported by shoutrrr, in its URL format: see github.com/nicholas-fedor/shoutrrr.' },
    ],
    priorityHelp: 'Only for services that take a priority; the format depends on the service.',
  },
]

// The service picker lists the services alphabetically, and the generic
// shoutrrr URL last.
export const PROVIDER_TYPES: ProviderTypeDef[] = [...TYPES].sort((a, b) =>
  Number(a.type === 'custom') - Number(b.type === 'custom')
  || a.label.localeCompare(b.label, undefined, { sensitivity: 'base' }))

export function providerTypeDef(type: string): ProviderTypeDef | undefined {
  return PROVIDER_TYPES.find((t) => t.type === type)
}
