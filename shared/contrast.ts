// WCAG 2.1 AA for normal text. Large text may use 3:1; these tokens are body size.
export const bodyContrastMinimum = 4.5

const channel = (value: number) => {
  const unit = value / 255
  return unit <= 0.04045 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4
}

const luminance = (red: number, green: number, blue: number) =>
  0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue)

export const parseHex = (color: string): [number, number, number] => {
  const trimmed = color.trim()
  const short = /^#([0-9a-f]{3})$/i.exec(trimmed)
  const full = short
    ? `#${short[1][0]}${short[1][0]}${short[1][1]}${short[1][1]}${short[1][2]}${short[1][2]}`
    : trimmed
  const match = /^#([0-9a-f]{6})$/i.exec(full)
  if (!match) throw new Error(`expected a hex color, got ${color}`)
  const value = Number.parseInt(match[1], 16)
  return [(value >> 16) & 255, (value >> 8) & 255, value & 255]
}

const hexByte = (value: number) => Math.round(value).toString(16).padStart(2, '0')

/** Opaque result of painting `ink` at `alpha` over `paper`. */
export const flatten = (ink: string, alpha: number, paper: string): string => {
  const [inkRed, inkGreen, inkBlue] = parseHex(ink)
  const [paperRed, paperGreen, paperBlue] = parseHex(paper)
  const mix = (front: number, back: number) => front * alpha + back * (1 - alpha)
  return `#${hexByte(mix(inkRed, paperRed))}${hexByte(mix(inkGreen, paperGreen))}${hexByte(mix(inkBlue, paperBlue))}`
}

export const contrastRatio = (foreground: string, background: string): number => {
  const [foregroundRed, foregroundGreen, foregroundBlue] = parseHex(foreground)
  const [backgroundRed, backgroundGreen, backgroundBlue] = parseHex(background)
  const lighter = Math.max(
    luminance(foregroundRed, foregroundGreen, foregroundBlue),
    luminance(backgroundRed, backgroundGreen, backgroundBlue),
  )
  const darker = Math.min(
    luminance(foregroundRed, foregroundGreen, foregroundBlue),
    luminance(backgroundRed, backgroundGreen, backgroundBlue),
  )
  return (lighter + 0.05) / (darker + 0.05)
}

/**
 * Darker palette steps are higher indexes. Returns the first index at or
 * after `start` that clears `minimum` against `background`, or -1 when
 * none do.
 */
export const readableShade = (
  colors: readonly string[],
  background: string,
  start: number,
  minimum = bodyContrastMinimum,
): number => {
  for (let index = Math.max(0, start); index < colors.length; index += 1) {
    if (contrastRatio(colors[index], background) >= minimum) return index
  }
  return -1
}

/**
 * Lighter palette steps are lower indexes. Returns the first index at or
 * before `start` that clears `minimum` against `background`, or -1 when
 * none do.
 */
export const readableLightShade = (
  colors: readonly string[],
  background: string,
  start: number,
  minimum = bodyContrastMinimum,
): number => {
  for (let index = Math.min(start, colors.length - 1); index >= 0; index -= 1) {
    if (contrastRatio(colors[index], background) >= minimum) return index
  }
  return -1
}

const mixChannel = (from: number, to: number, amount: number) =>
  Math.max(0, Math.min(255, Math.round(from + (to - from) * amount)))

/** Move `ink` toward `target` by `amount` (0 keeps ink, 1 is target). */
export const mix = (ink: string, target: string, amount: number): string => {
  const [inkRed, inkGreen, inkBlue] = parseHex(ink)
  const [targetRed, targetGreen, targetBlue] = parseHex(target)
  return `#${hexByte(mixChannel(inkRed, targetRed, amount))}${hexByte(mixChannel(inkGreen, targetGreen, amount))}${hexByte(mixChannel(inkBlue, targetBlue, amount))}`
}

/**
 * The same hue, moved toward `target` until it clears `minimum` on `background`.
 * Throws when even `target` cannot.
 */
export const towardContrast = (
  ink: string,
  target: string,
  background: string,
  minimum = bodyContrastMinimum,
): string => {
  if (contrastRatio(ink, background) >= minimum) return ink
  if (contrastRatio(target, background) < minimum) {
    throw new Error(`no color in this range reaches ${minimum}:1 on ${background}`)
  }
  let low = 0
  let high = 1
  for (let step = 0; step < 24; step += 1) {
    const mid = (low + high) / 2
    if (contrastRatio(mix(ink, target, mid), background) >= minimum) high = mid
    else low = mid
  }
  let amount = high
  let color = mix(ink, target, amount)
  while (contrastRatio(color, background) < minimum) {
    amount = Math.min(1, amount + 1 / 255)
    color = mix(ink, target, amount)
  }
  return color
}
