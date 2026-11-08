module.exports = async function sync({ github, context, core }) {
  const input = name => (process.env[`PROJECT_SYNC_${name}`] || '').trim();
  const list = name => input(name).split(/[,\n]/).map(value => value.trim().toLowerCase()).filter(Boolean);
  const boolean = name => {
    const value = input(name);
    if (value !== 'true' && value !== 'false') {
      throw new Error(`${name} must be true or false.`);
    }
    return value === 'true';
  };
  const positiveInteger = (value, name) => {
    if (!/^[1-9]\d*$/.test(String(value)) || !Number.isSafeInteger(Number(value))) {
      throw new Error(`${name} must be a positive safe integer.`);
    }
    return Number(value);
  };
  const finish = (result, reason, itemId = '') => {
    core.setOutput('sync-result', result);
    core.setOutput('reason', reason);
    core.setOutput('item-id', itemId);
    core.info(reason);
  };

  const projectOwner = input('OWNER');
  if (!projectOwner) throw new Error('project-owner is required.');
  const projectNumber = positiveInteger(input('NUMBER'), 'project-number');
  const types = list('TYPES');
  const states = list('STATES');
  if (!types.length || types.some(type => !['issue', 'pull_request'].includes(type))) {
    throw new Error('allowed-types must contain issue and/or pull_request.');
  }
  if (!states.length || states.some(state => !['open', 'closed'].includes(state))) {
    throw new Error('allowed-states must contain open and/or closed.');
  }
  const excludeDrafts = boolean('EXCLUDE_DRAFTS');
  const excludeBots = boolean('EXCLUDE_BOTS');
  const dryRun = boolean('DRY_RUN');
  const { owner, repo } = context.repo;
  const repositories = list('REPOSITORIES');
  if (repositories.some(repository => !/^[^/\s]+\/[^/\s]+$/.test(repository))) {
    throw new Error('allowed-repositories entries must use owner/repo format.');
  }
  if (repositories.length && !repositories.includes(`${owner}/${repo}`.toLowerCase())) {
    finish('skipped', 'Calling repository is not allowed.');
    return;
  }

  const eventItem = context.payload.issue || context.payload.pull_request;
  const numberInput = input('ISSUE_NUMBER') || eventItem?.number;
  if (numberInput === undefined) {
    finish('skipped', 'No issue or pull request in this event; supply issue-number for manual runs.');
    return;
  }
  const issueNumber = positiveInteger(numberInput, 'issue-number');
  const { data: issue } = await github.rest.issues.get({ owner, repo, issue_number: issueNumber });
  const type = issue.pull_request ? 'pull_request' : 'issue';
  if (!types.includes(type)) {
    finish('skipped', 'Item type is not allowed.');
    return;
  }
  if (!states.includes(issue.state)) {
    finish('skipped', 'Item state is not allowed.');
    return;
  }
  if (excludeBots && issue.user?.type === 'Bot') {
    finish('skipped', 'Bot-authored items are excluded.');
    return;
  }
  const authors = list('AUTHORS');
  if (authors.length && !authors.includes(issue.user?.login.toLowerCase())) {
    finish('skipped', 'Item author is not allowed.');
    return;
  }
  let contentId = issue.node_id;
  if (type === 'pull_request') {
    const { data: pull } = await github.rest.pulls.get({ owner, repo, pull_number: issueNumber });
    contentId = pull.node_id;
    if (excludeDrafts && pull.draft) {
      finish('skipped', 'Draft pull requests are excluded.');
      return;
    }
  }
  const requiredLabels = list('REQUIRED_LABELS');
  const excludedLabels = list('EXCLUDED_LABELS');
  if (requiredLabels.length || excludedLabels.length) {
    const labels = await github.paginate(github.rest.issues.listLabelsOnIssue, {
      owner, repo, issue_number: issueNumber, per_page: 100,
    });
    const names = new Set(labels.map(label => label.name.toLowerCase()));
    if (excludedLabels.some(label => names.has(label))) {
      finish('skipped', 'Item has an excluded label.');
      return;
    }
    if (!requiredLabels.every(label => names.has(label))) {
      finish('skipped', 'Item is missing a required label.');
      return;
    }
  }

  const data = await github.graphql(`
    query($owner: String!, $number: Int!) {
      repositoryOwner(login: $owner) {
        ... on Organization { projectV2(number: $number) { id closed } }
        ... on User { projectV2(number: $number) { id closed } }
      }
    }
  `, { owner: projectOwner, number: projectNumber });
  const project = data.repositoryOwner?.projectV2;
  if (!project) throw new Error('Project not found or token lacks access.');
  if (project.closed) {
    finish('skipped', 'Target project is closed.');
    return;
  }
  if (dryRun) {
    finish('dry-run', 'Item passed all filters; no project changes were made.');
    return;
  }
  const added = await github.graphql(`
    mutation($projectId: ID!, $contentId: ID!) {
      addProjectV2ItemById(input: { projectId: $projectId, contentId: $contentId }) {
        item { id }
      }
    }
  `, { projectId: project.id, contentId });
  const itemId = added.addProjectV2ItemById?.item?.id;
  if (!itemId) throw new Error('Project API did not return an item ID.');
  finish('synced', 'Item is present in the target project.', itemId);
};
