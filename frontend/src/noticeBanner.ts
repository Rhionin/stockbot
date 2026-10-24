// Light-scheme values of the web Mantine Alert (color="red", variant="light").
// The wash is #fa5252 at 10% over white. The message ink is --mantine-color-black.
// That alert's own border is transparent; the shared edge is the #EF4444 the
// notice reference uses for the border and the icon, on web and phone.
export const NOTICE_BANNER_TINT = '#ffeeee';
export const NOTICE_BANNER_TEXT = '#000000';
export const NOTICE_BANNER_EDGE = '#EF4444';
export const NOTICE_BANNER_RADIUS = 4;

// Same inset the sign-in screen already gives its content.
// LoginPage.css reads these variables, including the safe-area override,
// so the rendered inset and the tests share this one pair of numbers.
export const NOTICE_SCREEN_PADDING_TOP = 24;
export const NOTICE_SCREEN_PADDING_INLINE = 20;
export const NOTICE_SCREEN_PADDING_TOP_VAR = '--notice-screen-padding-top';
export const NOTICE_SCREEN_PADDING_INLINE_VAR = '--notice-screen-padding-inline';

if (typeof document !== 'undefined' && document.documentElement) {
  document.documentElement.style.setProperty(NOTICE_SCREEN_PADDING_TOP_VAR, `${NOTICE_SCREEN_PADDING_TOP}px`);
  document.documentElement.style.setProperty(NOTICE_SCREEN_PADDING_INLINE_VAR, `${NOTICE_SCREEN_PADDING_INLINE}px`);
}
