import type { Dict } from "./i18n/dictionaries/zh";

// formatAuthorRole localizes the GitHub author_association enum for display.
// The backend passes the raw value through (OWNER / CONTRIBUTOR / etc.); this is where it gets a language-specific label.
export function formatAuthorRole(role: string | undefined, t: Dict): string {
  if (!role) return "";
  const labels: Record<string, string> = {
    OWNER: t.ui.roleOwner,
    MEMBER: t.ui.roleMember,
    COLLABORATOR: t.ui.roleCollaborator,
    CONTRIBUTOR: t.ui.roleContributor,
    FIRST_TIMER: t.ui.roleFirstTimeContributor,
    FIRST_TIME_CONTRIBUTOR: t.ui.roleFirstTimeContributor,
    MANNEQUIN: t.ui.roleMannequin,
    NONE: t.ui.roleNone,
  };
  return labels[role.toUpperCase()] ?? role;
}
