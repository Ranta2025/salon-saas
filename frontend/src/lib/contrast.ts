/**
 * WCAG contrast helpers. They exist so the palette can be checked by a test
 * instead of by eye: body text must reach 4.5:1 on the surfaces it is used on.
 */

function parseHex(hex: string): [number, number, number] {
  const value = hex.trim().replace(/^#/, '')
  const expanded =
    value.length === 3
      ? value
          .split('')
          .map((char) => `${char}${char}`)
          .join('')
      : value
  if (!/^[0-9a-fA-F]{6}$/.test(expanded)) {
    throw new TypeError(`Color hexadecimal inválido: ${hex}`)
  }
  return [
    parseInt(expanded.slice(0, 2), 16),
    parseInt(expanded.slice(2, 4), 16),
    parseInt(expanded.slice(4, 6), 16),
  ]
}

function channelToLinear(channel: number): number {
  const value = channel / 255
  return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

/** WCAG 2.x relative luminance of an sRGB colour (`0` black, `1` white). */
export function relativeLuminance(hex: string): number {
  const [r, g, b] = parseHex(hex)
  return (
    0.2126 * channelToLinear(r) +
    0.7152 * channelToLinear(g) +
    0.0722 * channelToLinear(b)
  )
}

/** Contrast ratio between two sRGB colours, from 1:1 to 21:1. */
export function contrastRatio(first: string, second: string): number {
  const a = relativeLuminance(first)
  const b = relativeLuminance(second)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

/** AA for normal body text: 4.5:1. */
export function meetsWcagAa(first: string, second: string): boolean {
  return contrastRatio(first, second) >= 4.5
}
