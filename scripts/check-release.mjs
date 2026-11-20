import {execFileSync} from 'node:child_process';

const repository = 'makessr/WebDAVMount';
const [ref, extra] = process.argv.slice(2);
if (!ref || extra) {
  console.error('用法：bun run release:check <标签或提交 SHA>');
  process.exit(2);
}

function github(path) {
  return JSON.parse(execFileSync('gh', ['api', `repos/${repository}/${path}`], {encoding: 'utf8'}));
}

try {
  const {sha} = github(`commits/${encodeURIComponent(ref)}`);
  const {status} = github(`compare/${sha}...main`);
  if (!['identical', 'ahead'].includes(status)) {
    throw new Error('发行目标必须是 main 上的提交。');
  }
  const query = new URLSearchParams({branch: 'main', event: 'push', head_sha: sha, per_page: '1'});
  const {workflow_runs: [run]} = github(`actions/workflows/ci.yml/runs?${query}`);
  if (run?.head_sha !== sha || run.status !== 'completed' || run.conclusion !== 'success') {
    throw new Error('该提交最近一次 main CI 尚未成功。');
  }
  console.log(`发行门禁通过 ${sha} ${run.html_url}`);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
