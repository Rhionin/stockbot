// The browser keeps its session in an HttpOnly cookie, so nothing is stored in JS.
export async function readStored(_key: string): Promise<string | null> {
  return null;
}

export async function writeStored(_key: string, _value: string): Promise<void> {}

export async function deleteStored(_key: string): Promise<void> {}
