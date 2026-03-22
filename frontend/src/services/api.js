export const AUTH_UNAUTHORIZED_EVENT = 'auth:unauthorized'

export async function apiFetch(input, init = {}, options = {}) {
  const response = await fetch(input, {
    credentials: 'same-origin',
    ...init,
  })

  if (response.status === 401 && !options.suppressUnauthorizedRedirect) {
    window.dispatchEvent(new CustomEvent(AUTH_UNAUTHORIZED_EVENT))
  }

  return response
}
