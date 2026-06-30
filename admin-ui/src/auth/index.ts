// Single integration seam: the rest of the app imports `tokenProvider` from
// here. To plug in your own authentication, change this one line to export your
// implementation of the TokenProvider interface.
import { localTokenProvider } from "./localTokenProvider";
import type { TokenProvider } from "./types";

export const tokenProvider: TokenProvider = localTokenProvider;

export type { TokenProvider } from "./types";
export { localTokenProvider };
