// Messages for the oidc_error codes the OIDC callback redirects with, shown by
// the login page (sign-in) and the profile page (linking an account).
const OIDC_ERROR_MESSAGES: Record<string, string> = {
  access_denied: 'Your account is not in a group allowed to sign in to Arkeep. Contact your administrator.',
  account_exists:
    'An account with this email already exists. Sign in with your password, then connect single sign-on from your profile.',
  identity_in_use: 'This single sign-on identity is already connected to another Arkeep account.',
  disabled: 'Your account is disabled. Contact your administrator.',
  expired: 'The sign-in took too long or was interrupted. Please try again.',
}

// oidcErrorMessage returns the message for an oidc_error query value, or null
// when the query carries none.
export function oidcErrorMessage(code: unknown): string | null {
  if (typeof code !== 'string' || code === '') return null
  return OIDC_ERROR_MESSAGES[code] ?? 'Single sign-on failed. Please try again or contact your administrator.'
}
