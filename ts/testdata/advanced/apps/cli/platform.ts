import { basename } from 'node:path';

export const platformName = basename(process.platform);
