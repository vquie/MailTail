/**
 * @param {string} value
 * @returns {string}
 */
export function mailboxRoutingSummary (value) {
  const domains = value
    .split(/[\n\r,]+/)
    .map((entry) => entry.trim())
    .filter(Boolean)

  if (domains.length === 0) {
    return 'Not routed'
  }
  return `Routed · ${domains.join(', ')}`
}

/**
 * @param {string} value
 * @returns {boolean}
 */
export function mailboxIsRouted (value) {
  return value.trim().length > 0
}
