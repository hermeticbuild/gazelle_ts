import { protocolVersion } from "../shared/protocol";
import { generatedVersion } from "../ignored/generated";
export const version = protocolVersion + generatedVersion;
