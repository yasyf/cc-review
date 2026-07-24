import { useSession } from '../lib/api';
import { useReview } from '../lib/review-context';

// Rendered through CodeView's renderHeaderMetadata portal for a section banner
// item; like FileHeaderControls it self-subscribes to the session cache rather
// than receiving it via props.
export function SectionHeader({ sectionKey }: { sectionKey: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);

  if (!data) return null;
  const section = data.sections.find((s) => s.sectionKey === sectionKey);
  if (!section) return null;

  const total = section.files.length;
  const reviewed = section.files.filter((f) => section.fileStates[f.path]?.reviewed).length;

  return (
    <span className="section-controls">
      {section.pending ? <span className="section-pending">pending</span> : null}
      {section.parentBranch ? (
        <span className="section-parent" title={`parent: ${section.parentBranch}`}>
          ↑ {section.parentBranch}
        </span>
      ) : null}
      {!section.pending && section.baseRef && section.headRef ? (
        <span className="section-range">
          {section.baseRef.slice(0, 7)}..{section.headRef.slice(0, 7)}
        </span>
      ) : null}
      <span className="section-progress">
        {reviewed}/{total}
      </span>
    </span>
  );
}
