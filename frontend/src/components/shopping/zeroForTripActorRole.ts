import type { AuthSession } from '@stockbot/shared/contract';

/** Resolve owner vs member for the actor who set a trip zero (off-list who rules). */
export function zeroForTripActorRole(
  actorUserId: string | null | undefined,
  session: AuthSession | null | undefined,
  memberRoles?: Map<string, 'owner' | 'member'>,
): 'owner' | 'member' | null {
  if (actorUserId == null || actorUserId === '') return null;
  if (session?.user?.id === actorUserId) {
    const active = session.memberships.find((row) => row.householdId === session.activeHouseholdId);
    return active?.role ?? null;
  }
  return memberRoles?.get(actorUserId) ?? null;
}