import { prArtifactsContexts } from './contexts'
import type { Context, PRGroup } from '../types/api'

// Pins the helper signature: PRGroup[] in, Context[] out.
const groups: PRGroup[] = []
const out: Context[] = prArtifactsContexts(groups)
void out
