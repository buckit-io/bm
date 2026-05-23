// Shared validation rules for root user / root password inputs. Used by
// both the new-cluster wizard (Basics step) and the in-cluster
// "Rotate root password" operation. The Go-side equivalent lives in
// internal/credentials; keep both in sync.

export const ROOT_PASSWORD_MIN_LEN = 8;
export const ROOT_PASSWORD_MAX_LEN = 40;
export const ROOT_USER_MIN_LEN = 3;

export const ROOT_PASSWORD_HINT = `${ROOT_PASSWORD_MIN_LEN}-${ROOT_PASSWORD_MAX_LEN} printable ASCII characters, no spaces. 16+ recommended.`;
export const ROOT_USER_HINT = `Letters, digits, underscore, and dash only. Min ${ROOT_USER_MIN_LEN} characters.`;

const PRINTABLE_ASCII_NO_SPACES = /^[!-~]+$/;
const ROOT_USER_CHARSET = /^[a-zA-Z0-9_-]+$/;

export type Validation = { ok: true } | { ok: false; error: string };

export function validateRootPassword(pw: string): Validation {
  if (pw.length < ROOT_PASSWORD_MIN_LEN || pw.length > ROOT_PASSWORD_MAX_LEN) {
    return {
      ok: false,
      error: `Password must be ${ROOT_PASSWORD_MIN_LEN}-${ROOT_PASSWORD_MAX_LEN} characters (currently ${pw.length}).`,
    };
  }
  if (!PRINTABLE_ASCII_NO_SPACES.test(pw)) {
    return {
      ok: false,
      error: "Password must use printable ASCII characters without spaces.",
    };
  }
  return { ok: true };
}

export function validateRootUser(user: string): Validation {
  if (user.length < ROOT_USER_MIN_LEN) {
    return {
      ok: false,
      error: `Username must be at least ${ROOT_USER_MIN_LEN} characters (currently ${user.length}).`,
    };
  }
  if (!ROOT_USER_CHARSET.test(user)) {
    return {
      ok: false,
      error: "Username may only contain letters, digits, underscore, or dash.",
    };
  }
  return { ok: true };
}
