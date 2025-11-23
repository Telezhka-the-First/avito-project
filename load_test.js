import http from "k6/http"
import { check, sleep } from "k6"
import { randomItem } from "https://jslib.k6.io/k6-utils/1.4.0/index.js"

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080"

export const options = {
  scenarios: {
    main: {
      executor: "constant-arrival-rate",
      rate: 5,
      duration: __ENV.DURATION || "30s",
      preAllocatedVUs: 5,
      maxVUs: 20,
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<300"],
    http_req_failed: ["rate<0.001"],
  },
}

const jsonHeaders = {
  headers: {
    "Content-Type": "application/json",
  },
}

export function setup() {
  const teamsCount = Number(__ENV.TEAMS || 20)
  const usersPerTeam = Number(__ENV.USERS_PER_TEAM || 10)
  const allUserIds = []

  for (let t = 1; t <= teamsCount; t++) {
    const suffix = Math.random().toString(36).substring(2, 8)
    const teamName = `load-team-${t}-${suffix}`

    const members = []
    for (let u = 1; u <= usersPerTeam; u++) {
      const userId = `t${t}u${u}`
      members.push({
        user_id: userId,
        username: `User ${t}-${u}`,
        is_active: true,
      })
      allUserIds.push(userId)
    }

    const payload = JSON.stringify({
      team_name: teamName,
      members,
    })

    const res = http.post(`${BASE_URL}/team/add`, payload, jsonHeaders)

    check(res, {
      "team add status is 201": (r) => r.status === 201,
    })

    if (res.status !== 201) {
      throw new Error(`setup failed for team ${teamName}: ${res.status} ${res.body}`)
    }
  }

  return {
    userIds: allUserIds,
  }
}

export default function (data) {
  const { userIds } = data

  const prId = `pr-${__VU}-${__ITER}-${Date.now()}`
  const authorId = randomItem(userIds)
  const prName = `Load test PR ${prId}`

  const createRes = http.post(
    `${BASE_URL}/pullRequest/create`,
    JSON.stringify({
      pull_request_id: prId,
      pull_request_name: prName,
      author_id: authorId,
    }),
    jsonHeaders,
  )

  check(createRes, {
    "create PR status is 201": (r) => r.status === 201,
  })

  let assignedReviewers = []
  if (createRes.status === 201) {
    try {
      const body = JSON.parse(createRes.body)
      if (body.pr && Array.isArray(body.pr.assigned_reviewers)) {
        assignedReviewers = body.pr.assigned_reviewers
      }
    } catch (e) {}
  }

  if (assignedReviewers.length > 0) {
    const oldUserId = assignedReviewers[0]

    const reassignRes = http.post(
      `${BASE_URL}/pullRequest/reassign`,
      JSON.stringify({
        pull_request_id: prId,
        old_user_id: oldUserId,
      }),
      jsonHeaders,
    )

    check(reassignRes, {
      "reassign status is 200": (r) => r.status === 200,
    })
  }

  const mergeRes = http.post(
    `${BASE_URL}/pullRequest/merge`,
    JSON.stringify({ pull_request_id: prId }),
    jsonHeaders,
  )

  check(mergeRes, {
    "merge status is 200": (r) => r.status === 200,
  })

  const reviewerId = randomItem(userIds)
  const getReviewRes = http.get(
    `${BASE_URL}/users/getReview?user_id=${encodeURIComponent(reviewerId)}`,
  )

  check(getReviewRes, {
    "getReview status is 200": (r) => r.status === 200,
  })

  const statsRes = http.get(`${BASE_URL}/stats/assignments`)

  check(statsRes, {
    "stats status is 200": (r) => r.status === 200,
  })

  const pause = Number(__ENV.PAUSE || 0.1)
  if (pause > 0) {
    sleep(pause)
  }
}
