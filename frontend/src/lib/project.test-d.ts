import { isPRProject, projectInContext } from './project'

const a: boolean = isPRProject('PR:pr-92:ppg:staging:17')
const b: boolean = projectInContext('ppg:common', 'ppg:staging')
const c: boolean = projectInContext('PR:pr-92:common', 'PR:pr-92:ppg:staging')
void a; void b; void c
