#!/usr/bin/env node
import { execSync } from 'node:child_process'

const specs = [
  { spec: '../api/command/openapi.yaml', out: 'src/api/command.types.ts' },
  { spec: '../api/query/openapi.yaml', out: 'src/api/query.types.ts' },
]

for (const { spec, out } of specs) {
  console.log(`Generating ${out} from ${spec}...`)
  execSync(`npx openapi-typescript ${spec} -o ${out}`, { stdio: 'inherit' })
}
