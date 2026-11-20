import {readFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';

const {pull_request: pr} = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
const body = (pr.body ?? '').replace(/<!--[\s\S]*?-->/g, '');
if (!/\p{Script=Han}/u.test(pr.title) || !/\p{Script=Han}/u.test(body)) {
  throw new Error('Pull Request 标题和说明须使用中文。');
}

const sections = body.split(/^## /m);
for (const section of ['用户可见变化', '已执行的验证']) {
  const content = sections.find(part => part.split('\n', 1)[0].trim() === section);
  if (!content?.split('\n').slice(1).join('\n').trim()) {
    throw new Error(`请填写「${section}」。`);
  }
}

const git = (...args) => execFileSync('git', args, {encoding: 'utf8'}).trim();
const commits = git('rev-list', '--no-merges', `${pr.base.sha}..${pr.head.sha}`).split('\n').filter(Boolean);
for (const sha of commits) {
  if (git('diff-tree', '--no-commit-id', '--name-only', '-r', sha, '--', 'README.md') !== 'README.md') {
    throw new Error(`提交 ${sha.slice(0, 8)} 缺少 README.md 更新。`);
  }
}
