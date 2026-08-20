# Complement coverage

Suite: `./tests/csapi` at complement `6d2fdc2`.
Federation tests are not run: this server does not implement federation.

| tests | pass | fail | skip | passing |
|------:|-----:|-----:|-----:|--------:|
| 106 | 1 | 103 | 2 | 0.9% |

## Passing

- TestVersionStructure

## Skipped

- TestCanRegisterAdmin
- TestServerNotices

## Why the failures stop where they do

Endpoints the suite asked for, most requested first. Whatever sits at the top is what
the rest of the suite is waiting on.

- 128 x POST hs1/_matrix/client/v3/register
- 6 x GET hs1/_synapse/admin/v1/register
- 2 x GET hs1/_matrix/client/v3/register/available
- 1 x GET hs1/_matrix/client/versions

Most frequent assertion failures:

- 119 x Expected 401 Unauthorized, got 404

## Failing

- TestAddAccountData
- TestArchivedRoomsHistory
- TestAsyncUpload
- TestAvatarUrlUpdate
- TestCannotKickLeftUser
- TestCannotKickNonPresentUser
- TestChangePassword
- TestChangePasswordPushers
- TestContent
- TestContentCSAPIMediaV1
- TestCumulativeJoinLeaveJoinSync
- TestDeactivateAccount
- TestDemotingUsersViaUsersDefault
- TestDeviceListUpdates
- TestDeviceManagement
- TestDisplayNameUpdate
- TestE2EKeyBackupReplaceRoomKeyRules
- TestEvent
- TestFetchEvent
- TestFetchEventNonWorldReadable
- TestFetchEventWorldReadable
- TestFetchHistoricalInvitedEventFromBeforeInvite
- TestFetchHistoricalInvitedEventFromBetweenInvite
- TestFetchHistoricalJoinedEventDenied
- TestFetchHistoricalSharedEvent
- TestFetchMessagesFromNonExistentRoom
- TestFilter
- TestGappedSyncLeaveSection
- TestGetFilteredRoomMembers
- TestGetRoomMembers
- TestGetRoomMembersAtPoint
- TestInviteFromIgnoredUsersDoesNotAppearInSync
- TestJson
- TestKeyChangesLocal
- TestKeyClaimOrdering
- TestKeysQueryWithDeviceIDAsObjectFails
- TestLeakyTyping
- TestLeaveEventInviteRejection
- TestLeaveEventVisibility
- TestLeftRoomFixture
- TestLogin
- TestLogout
- TestMediaConfig
- TestMembersLocal
- TestMembershipOnEvents
- TestMessagesOverFederation
- TestNotPresentUserCannotBanOthers
- TestOlderLeftRoomsNotInLeaveSection
- TestPowerLevels
- TestPresence
- TestPresenceSyncDifferentRooms
- TestProfileAvatarURL
- TestProfileDisplayName
- TestPublicRooms
- TestPushRuleCacheHealth
- TestPushRuleRoomUpgrade
- TestPushSync
- TestRedact
- TestRegistration
- TestRelations
- TestRelationsPagination
- TestRelationsPaginationSync
- TestRequestEncodingFails
- TestRoomAlias
- TestRoomCanonicalAlias
- TestRoomCreate
- TestRoomCreationReportsEventsToMyself
- TestRoomDeleteAlias
- TestRoomForget
- TestRoomImageRoundtrip
- TestRoomMembers
- TestRoomMessagesLazyLoading
- TestRoomMessagesLazyLoadingLocalUser
- TestRoomReadMarkers
- TestRoomReceipts
- TestRoomSpecificUsernameAtJoin
- TestRoomSpecificUsernameChange
- TestRoomState
- TestRoomSummary
- TestRoomsInvite
- TestSearch
- TestSendAndFetchMessage
- TestSendMessageWithTxn
- TestServerCapabilities
- TestSync
- TestSyncFilter
- TestSyncLeaveSection
- TestSyncTimelineGap
- TestTentativeEventualJoiningAfterRejecting
- TestThreadReceiptsInSyncMSC4102
- TestThreadedReceipts
- TestThreadsEndpoint
- TestToDeviceMessages
- TestTxnIdWithRefreshToken
- TestTxnIdempotency
- TestTxnIdempotencyScopedToDevice
- TestTxnInEvent
- TestTxnScopeOnLocalEcho
- TestTyping
- TestUploadKey
- TestUploadKeyIdempotency
- TestUploadKeyIdempotencyOverlap
- TestUrlPreview
