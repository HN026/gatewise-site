import { CodeBlock, Note, PageTitle, Section, Table } from '../components/ui';
import { latestRelease, releases, repositoryUrl } from '../data/releases';

export default function Versions() {
  return <div className="container">
    <PageTitle title="Versions">Release history, version requirements and downloads for Gatewise.</PageTitle>
    <Section id="latest" title="Latest release">
      <p><strong>{latestRelease.version}</strong> · Published <time dateTime={latestRelease.publishedAt}>{latestRelease.date}</time> (IST)</p>
      <p>{latestRelease.summary}</p>
      <Table headers={['Requirement', 'Value']} rows={[
        ['Go toolchain', latestRelease.requirements],
        ['Go module', 'github.com/HN026/gatewise'],
        ['Integrations', 'Go middleware, HTTP proxy, worker and SQL adapters'],
        ['License', 'MIT'],
      ]} />
      <a className="text-link" href={`${repositoryUrl}/releases/tag/${latestRelease.version}`}>View release on GitHub ↗</a>
    </Section>
    <Section id="install-version" title="Install a version">
      <p>Pin a release in your application module. After adding Gatewise imports and handler integration, resolve dependencies:</p>
      <CodeBlock filename="Go application" language="shell" code={`go get github.com/HN026/gatewise@${latestRelease.version}\ngo mod tidy`} />
      <p>To build the standalone proxy from the same release:</p>
      <CodeBlock filename="Standalone proxy" language="shell" code={`git clone --branch ${latestRelease.version} https://github.com/HN026/gatewise.git\ncd gatewise\ngo build -o bin/gatewise ./cmd/gatewise`} />
      <p>See <a href="#/installation">Installation &amp; deployment</a> for handler integration, proxy configuration and verification. Demo startup instructions are in <a href="#/examples">Examples</a>.</p>
    </Section>
    <Section id="release-history" title="Release history">
      {releases.map(release => <article key={release.version} id={release.version}>
        <h3>{release.version}</h3>
        <p><time dateTime={release.publishedAt}>{release.date}</time> (IST) · {release.requirements}</p>
        <p>{release.summary}</p>
        <ul className="technical-list">{release.changes.map(change => <li key={change}>{change}</li>)}</ul>
        <p><strong>Upgrade notes:</strong> {release.upgrade}</p>
        <div className="download-links">{release.downloads.map(download => <a key={download.name} href={`${repositoryUrl}/${download.path}`}>{download.name} ↗</a>)}</div>
        <p><a href={`${repositoryUrl}/releases/tag/${release.version}`}>GitHub release notes ↗</a></p>
      </article>)}
    </Section>
    <Section id="upgrading" title="Upgrading">
      <p>Read the target release notes, update the pinned module version, run <code>go mod tidy</code>, and validate your application before deployment. For the proxy, build the target tag and verify the configuration and route behavior before replacing the running binary.</p>
      <Note>Gatewise v1 follows the documented compatibility contract. Review release notes and optional policy changes before upgrading. Concurrency limits apply per process; a new release does not change how replica budgets must be sized.</Note>
      <a className="text-link" href={`${repositoryUrl}/releases`}>All GitHub releases ↗</a>
    </Section>
  </div>;
}
