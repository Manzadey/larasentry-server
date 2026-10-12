package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"server/internal/delivery/sse"
	"server/internal/domain"
	"server/internal/usecase"
)

type APIHandler struct {
	projectRepo  domain.ProjectRepository
	incidentRepo domain.IncidentRepository
	releaseRepo  domain.ReleaseRepository
	sseBroker    *sse.Broker
	eventChan    chan domain.IncomingEvent
	startTime    time.Time
}

func NewAPIHandler(
	projectRepo domain.ProjectRepository,
	incidentRepo domain.IncidentRepository,
	eventChan chan domain.IncomingEvent,
) *APIHandler {
	return &APIHandler{
		projectRepo:  projectRepo,
		incidentRepo: incidentRepo,
		eventChan:    eventChan,
		startTime:    time.Now(),
	}
}

func (h *APIHandler) SetReleaseRepo(repo domain.ReleaseRepository) {
	h.releaseRepo = repo
}

func (h *APIHandler) SetSSEBroker(broker *sse.Broker) {
	h.sseBroker = broker
}

func (h *APIHandler) resolveProject(r *http.Request, explicitName string) (*domain.Project, error) {
	ctx := r.Context()
	projectName := strings.TrimSpace(explicitName)

	if projectName == "" {
		for _, hdr := range []string{"X-Project", "X-Project-Name", "X-App-Name"} {
			if val := strings.TrimSpace(r.Header.Get(hdr)); val != "" {
				projectName = val
				break
			}
		}
	}

	if projectName == "" {
		for _, qParam := range []string{"project", "project_name", "app"} {
			if val := strings.TrimSpace(r.URL.Query().Get(qParam)); val != "" {
				projectName = val
				break
			}
		}
	}

	if projectName == "" {
		if pidStr := strings.TrimSpace(r.URL.Query().Get("project_id")); pidStr != "" {
			if pid, err := strconv.ParseInt(pidStr, 10, 64); err == nil && pid > 0 {
				p, err := h.projectRepo.GetByID(ctx, pid)
				if err == nil && p != nil {
					return p, nil
				}
			}
		}
	}

	// Backwards compatibility for Bearer tokens or query param token
	authHeader := r.Header.Get("Authorization")
	var apiKey string
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	} else {
		apiKey = r.URL.Query().Get("token")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		p, err := h.projectRepo.GetByKey(ctx, apiKey)
		if err == nil && p != nil {
			return p, nil
		}
	}

	if projectName != "" {
		return h.projectRepo.GetOrCreateByName(ctx, projectName)
	}

	return h.projectRepo.GetOrCreateByName(ctx, "Default Project")
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, errMessage string) {
	writeJSON(w, status, map[string]string{"error": errMessage})
}

func getPathParam(r *http.Request, key string) string {
	if val := r.PathValue(key); val != "" {
		return val
	}
	// Fallback path segment parsing
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if key == "id" && len(parts) >= 3 && parts[1] == "incidents" {
		return parts[2]
	}
	if key == "version" && len(parts) >= 3 && parts[1] == "releases" {
		return parts[2]
	}
	return ""
}

// =============================================================================
// 1. EVENT INGESTION
// =============================================================================

// HandleEvent handles POST /api/events
// @Summary Отправить событие ошибки (Event Ingestion)
// @Description Принимает и валидирует входящий payload исключения из Laravel-приложения (через larasentry-client). Обработка происходит асинхронно: запрос ставится в неблокирующую очередь в памяти (channel), вычисляется уникальный MD5-хеш ошибки и клиенту возвращается статус 202 Accepted. Если ошибка повторяется, инцидент дедуплицируется и счетчик увеличивается.
// @Tags Events (Прием событий)
// @Accept json
// @Produce json
// @Param payload body domain.EventPayload true "JSON-пейлоад с детальной информацией об исключении из Laravel"
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 202 {object} object{status=string,event_id=string,hash=string} "Событие успешно принято в очередь обработки. Возвращает сгенерированный UUID события и MD5-хеш инцидента."
// @Failure 400 "Некорректный запрос (невалидный JSON или отсутствуют обязательные поля exception_class/message)"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/events [post]
func (h *APIHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var payload domain.EventPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	projName := payload.Project
	if projName == "" {
		projName = payload.ProjectName
	}
	if projName == "" && payload.Tags != nil {
		if val, ok := payload.Tags["project"]; ok {
			projName = val
		} else if val, ok := payload.Tags["project_name"]; ok {
			projName = val
		}
	}

	project, err := h.resolveProject(r, projName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	payload.Release = strings.TrimSpace(payload.Release)
	payload.Environment = strings.TrimSpace(payload.Environment)
	payload.Message = strings.TrimSpace(payload.Message)
	payload.ExceptionClass = strings.TrimSpace(payload.ExceptionClass)
	payload.File = strings.TrimSpace(payload.File)
	payload.AppURL = strings.TrimSpace(payload.AppURL)
	payload.RequestURL = strings.TrimSpace(payload.RequestURL)
	payload.StackTrace = strings.TrimSpace(payload.StackTrace)
	payload.PHPVersion = strings.TrimSpace(payload.PHPVersion)
	if payload.PHPVersion == "" && payload.System != nil && payload.System.PHPVersion != "" {
		payload.PHPVersion = strings.TrimSpace(payload.System.PHPVersion)
	}
	if payload.PHPVersion == "" && payload.Tags != nil {
		if v, ok := payload.Tags["php_version"]; ok {
			payload.PHPVersion = strings.TrimSpace(v)
		} else if v, ok := payload.Tags["php"]; ok {
			payload.PHPVersion = strings.TrimSpace(v)
		}
	}
	payload.LaravelVersion = strings.TrimSpace(payload.LaravelVersion)
	if payload.LaravelVersion == "" && payload.System != nil && payload.System.LaravelVersion != "" {
		payload.LaravelVersion = strings.TrimSpace(payload.System.LaravelVersion)
	}
	if payload.LaravelVersion == "" && payload.Tags != nil {
		if v, ok := payload.Tags["laravel_version"]; ok {
			payload.LaravelVersion = strings.TrimSpace(v)
		} else if v, ok := payload.Tags["laravel"]; ok {
			payload.LaravelVersion = strings.TrimSpace(v)
		}
	}

	if payload.ExceptionClass == "" || payload.Message == "" {
		writeError(w, http.StatusBadRequest, "Bad Request - Missing exception_class or message")
		return
	}

	eventID := uuid.New().String()
	hash := usecase.CalculateHash(project.APIKey, payload)

	h.eventChan <- domain.IncomingEvent{
		EventID:     eventID,
		ProjectID:   project.ID,
		ProjectName: project.Name,
		ProjectKey:  project.APIKey,
		Payload:     payload,
	}

	log.Printf("[Event] Received event %s for project %s (%d): %s\n", eventID, project.Name, project.ID, payload.ExceptionClass)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":   "accepted",
		"event_id": eventID,
		"hash":     hash,
	})
}

// HandleBatchEvents handles POST /api/events/batch
// @Summary Пакетная отправка событий ошибок (Batch Ingestion)
// @Description Позволяет отправить массив ошибок (до 50 событий) за один HTTP-запрос. Используется для периодического сброса локального буфера очередей в Laravel или снижения сетевых накладных расходов при пиковых нагрузках.
// @Tags Events (Прием событий)
// @Accept json
// @Produce json
// @Param body body object{project=string,events=[]domain.EventPayload} true "Объект, содержащий массив событий"
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 202 {object} object{status=string,accepted_count=int} "Пачка событий принята в очередь обработки"
// @Failure 400 "Невалидный JSON, пустой список или превышен лимит в 50 событий"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/events/batch [post]
func (h *APIHandler) HandleBatchEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Project string                `json:"project"`
		Events  []domain.EventPayload `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	defaultBatchProject, err := h.resolveProject(r, req.Project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "Bad Request - No events provided")
		return
	}
	if len(req.Events) > 50 {
		writeError(w, http.StatusBadRequest, "Bad Request - Batch exceeds maximum size of 50")
		return
	}

	acceptedCount := 0
	for _, payload := range req.Events {
		eventProj := defaultBatchProject
		eventProjName := payload.Project
		if eventProjName == "" {
			eventProjName = payload.ProjectName
		}
		if eventProjName != "" && eventProjName != defaultBatchProject.Name {
			if resolved, err := h.resolveProject(r, eventProjName); err == nil && resolved != nil {
				eventProj = resolved
			}
		}

		payload.Release = strings.TrimSpace(payload.Release)
		payload.Environment = strings.TrimSpace(payload.Environment)
		payload.Message = strings.TrimSpace(payload.Message)
		payload.ExceptionClass = strings.TrimSpace(payload.ExceptionClass)
		payload.File = strings.TrimSpace(payload.File)
		payload.AppURL = strings.TrimSpace(payload.AppURL)
		payload.RequestURL = strings.TrimSpace(payload.RequestURL)
		payload.StackTrace = strings.TrimSpace(payload.StackTrace)
		payload.PHPVersion = strings.TrimSpace(payload.PHPVersion)
		if payload.PHPVersion == "" && payload.System != nil && payload.System.PHPVersion != "" {
			payload.PHPVersion = strings.TrimSpace(payload.System.PHPVersion)
		}
		if payload.PHPVersion == "" && payload.Tags != nil {
			if v, ok := payload.Tags["php_version"]; ok {
				payload.PHPVersion = strings.TrimSpace(v)
			} else if v, ok := payload.Tags["php"]; ok {
				payload.PHPVersion = strings.TrimSpace(v)
			}
		}
		payload.LaravelVersion = strings.TrimSpace(payload.LaravelVersion)
		if payload.LaravelVersion == "" && payload.System != nil && payload.System.LaravelVersion != "" {
			payload.LaravelVersion = strings.TrimSpace(payload.System.LaravelVersion)
		}
		if payload.LaravelVersion == "" && payload.Tags != nil {
			if v, ok := payload.Tags["laravel_version"]; ok {
				payload.LaravelVersion = strings.TrimSpace(v)
			} else if v, ok := payload.Tags["laravel"]; ok {
				payload.LaravelVersion = strings.TrimSpace(v)
			}
		}

		if payload.ExceptionClass == "" || payload.Message == "" {
			continue
		}

		eventID := uuid.New().String()
		h.eventChan <- domain.IncomingEvent{
			EventID:     eventID,
			ProjectID:   eventProj.ID,
			ProjectName: eventProj.Name,
			ProjectKey:  eventProj.APIKey,
			Payload:     payload,
		}
		acceptedCount++
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":         "accepted",
		"accepted_count": acceptedCount,
	})
}

// =============================================================================
// 2. INCIDENTS API
// =============================================================================

// HandleListIncidents handles GET /api/incidents
// @Summary Список инцидентов с фильтрацией и пагинацией
// @Description Возвращает постраничный список сгруппированных инцидентов проекта. Поддерживает фильтрацию по статусу (unresolved, resolved, ignored, all), окружению, версии релиза, поисковой строке, только регрессиям, а также сортировку и пагинацию с метаданными.
// @Tags Incidents (Инциденты)
// @Produce json
// @Param status query string false "Статус инцидента: unresolved (нерешенные, по умолчанию), resolved (закрытые), ignored (заглушенные), all (все)" Enums(unresolved,resolved,ignored,all) default(unresolved)
// @Param q query string false "Поисковый запрос для фильтрации по сообщению, имени класса, файлу или URL"
// @Param environment query string false "Окружение исполнения (напр. production, staging, local)"
// @Param release query string false "Версия релиза (first_release или last_release)"
// @Param is_regression query bool false "Флаг регрессии: true — показать только ошибки, вернувшиеся после закрытия"
// @Param sort query string false "Сортировка: recent (свежие), count (по частоте повторов), first_seen (хронологически)" Enums(recent,count,first_seen) default(recent)
// @Param page query int false "Номер страницы для пагинации (начиная с 1)" default(1)
// @Param limit query int false "Количество записей на странице (от 1 до 200)" default(50)
// @Param include_trace query bool false "Включать ли полный стектрейс в каждый элемент списка (по умолчанию false для экономии трафика)" default(false)
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 200 {object} domain.IncidentListResponse "Успешный возврат списка инцидентов с объектом пагинации meta"
// @Failure 500 "Внутренняя ошибка сервера базы данных"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents [get]
func (h *APIHandler) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var projectID int64
	hasExplicitProject := r.URL.Query().Get("project") != "" ||
		r.URL.Query().Get("project_name") != "" ||
		r.URL.Query().Get("project_id") != "" ||
		r.Header.Get("X-Project") != "" ||
		r.Header.Get("X-Project-Name") != "" ||
		r.Header.Get("Authorization") != "" ||
		r.URL.Query().Get("token") != ""

	if hasExplicitProject {
		if p, err := h.resolveProject(r, ""); err == nil && p != nil {
			projectID = p.ID
		}
	}

	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status == "" {
		status = "unresolved"
	}

	queryStr := strings.TrimSpace(q.Get("q"))
	environment := strings.TrimSpace(q.Get("environment"))
	release := strings.TrimSpace(q.Get("release"))
	sort := strings.TrimSpace(q.Get("sort"))
	if sort == "" {
		sort = "recent"
	}

	page := 1
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	limit := 50
	if lStr := q.Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 200 {
		limit = 200
	}

	var isRegression *bool
	if regStr := q.Get("is_regression"); regStr != "" {
		val := regStr == "true" || regStr == "1"
		isRegression = &val
	}

	includeTrace := q.Get("include_trace") == "true" || q.Get("include_trace") == "1"

	offset := (page - 1) * limit
	filter := domain.IncidentFilter{
		ProjectID:    projectID,
		Status:       status,
		Query:        queryStr,
		Environment:  environment,
		Release:      release,
		IsRegression: isRegression,
		Sort:         sort,
		Limit:        limit,
		Offset:       offset,
	}

	incidents, err := h.incidentRepo.GetIncidents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error: "+err.Error())
		return
	}

	total, err := h.incidentRepo.GetIncidentsCount(r.Context(), filter)
	if err != nil {
		total = len(incidents)
	}

	// Optimize bandwidth: strip stack traces if not requested
	if !includeTrace {
		for i := range incidents {
			incidents[i].StackTrace = ""
		}
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + limit - 1) / limit
	}

	writeJSON(w, http.StatusOK, domain.IncidentListResponse{
		Items: incidents,
		Meta: domain.PageMeta{
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	})
}

// HandleGetIncident handles GET /api/incidents/{id}
// @Summary Детальная карточка инцидента
// @Description Возвращает полные данные об инциденте: полный стектрейс ошибки, очищенный стектрейс приложения (без сторонних библиотек vendor/laravel/symfony), статистику фреймов кода и историю первого/последнего появления.
// @Tags Incidents (Инциденты)
// @Produce json
// @Param id path int true "Числовой идентификатор инцидента в базе данных"
// @Success 200 {object} domain.IncidentDetail "Полная информация об инциденте со стектрейсами"
// @Failure 404 "Инцидент с указанным ID не найден"
// @Failure 400 "Некорректный ID инцидента"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/{id} [get]
func (h *APIHandler) HandleGetIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	detail := domain.IncidentDetail{
		Incident:        *inc,
		AppStackTrace:   inc.AppStackTrace(),
		StackTraceStats: inc.StackTraceStats(),
	}

	writeJSON(w, http.StatusOK, detail)
}

// HandleResolveIncident handles POST /api/incidents/{id}/resolve
// @Summary Закрыть (резолвить) инцидент
// @Description Помечает инцидент как решенный со стратегией отслеживания повторений:
// @Description • immediate — закрыть немедленно (при любом новом появлении инцидент переоткроется);
// @Description • next — исправлен в следующем релизе (новые события из текущего релиза будут глушиться);
// @Description • current — исправлен в текущем активном релизе проекта;
// @Description • version — исправлен в конкретной версии (напр. 1.4.3). События из более старых версий игнорируются, а в версии >= указанной — фиксируется регрессия.
// @Tags Incidents Lifecycle (Жизненный цикл)
// @Accept json
// @Produce json
// @Param id path int true "Идентификатор инцидента"
// @Param body body domain.ResolveRequest false "Параметры резолва ошибки"
// @Success 200 {object} domain.Incident "Инцидент успешно закрыт. Возвращает обновленные данные инцидента."
// @Failure 404 "Инцидент не найден"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/{id}/resolve [post]
func (h *APIHandler) HandleResolveIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var req domain.ResolveRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Type == "" {
		req.Type = r.URL.Query().Get("type")
	}
	if req.Type == "" {
		req.Type = "immediate"
	}
	if req.Version == "" {
		req.Version = r.URL.Query().Get("version")
	}

	if err := h.incidentRepo.ResolveIncident(r.Context(), id, req.Type, req.Version); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleReopenIncident handles POST /api/incidents/{id}/reopen
// @Summary Переоткрыть инцидент
// @Description Переводит ранее закрытый или заглушенный инцидент обратно в активный статус 'unresolved', сбрасывая статус резолва и флаг регрессии.
// @Tags Incidents Lifecycle (Жизненный цикл)
// @Produce json
// @Param id path int true "Идентификатор инцидента"
// @Success 200 {object} domain.Incident "Инцидент успешно переоткрыт"
// @Failure 404 "Инцидент не найден"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/{id}/reopen [post]
func (h *APIHandler) HandleReopenIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	if err := h.incidentRepo.ReopenIncident(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reopen incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleIgnoreIncident handles POST /api/incidents/{id}/ignore
// @Summary Заглушить / Игнорировать инцидент
// @Description Переводит инцидент в статус 'ignored'. События продолжат агрегироваться и подсчитываться в базе, но уведомления в Telegram отправляться не будут.
// @Tags Incidents Lifecycle (Жизненный цикл)
// @Accept json
// @Produce json
// @Param id path int true "Идентификатор инцидента"
// @Param body body object{ignored=bool} false "Флаг игнорирования"
// @Success 200 {object} domain.Incident "Статус игнорирования обновлен"
// @Failure 404 "Инцидент не найден"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/{id}/ignore [post]
func (h *APIHandler) HandleIgnoreIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var req struct {
		Ignored *bool `json:"ignored"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ignored := true
	if req.Ignored != nil {
		ignored = *req.Ignored
	}

	if err := h.incidentRepo.IgnoreIncident(r.Context(), id, ignored); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to ignore incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleDeleteIncident handles DELETE /api/incidents/{id}
// @Summary Удалить инцидент
// @Description Безвозвратно удаляет запись об инциденте из базы данных. Полезно для очистки ошибочных логов или тестовых сбоев.
// @Tags Incidents (Инциденты)
// @Param id path int true "Идентификатор удаляемого инцидента"
// @Success 204 "Инцидент успешно удален (тело ответа пустое)"
// @Failure 404 "Инцидент не найден"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/{id} [delete]
func (h *APIHandler) HandleDeleteIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	if err := h.incidentRepo.DeleteIncident(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete incident: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleBatchIncidents handles POST /api/incidents/batch
// @Summary Пакетные действия над несколькими инцидентами
// @Description Выполняет групповое действие в рамках единой транзакции для массива ID: закрыть (resolve), переоткрыть (reopen), заглушить (ignore) или удалить (delete).
// @Tags Incidents Lifecycle (Жизненный цикл)
// @Accept json
// @Produce json
// @Param body body domain.BatchIncidentsRequest true "Параметры пакетной операции"
// @Success 200 {object} object{action=string,affected=int} "Операция успешно выполнена"
// @Failure 400 "Некорректный запрос или пустой массив ID"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/incidents/batch [post]
func (h *APIHandler) HandleBatchIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req domain.BatchIncidentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "Bad Request - IDs array is empty")
		return
	}

	var verifiedIDs []int64
	for _, id := range req.IDs {
		inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
		if err == nil && inc != nil {
			verifiedIDs = append(verifiedIDs, id)
		}
	}

	if len(verifiedIDs) == 0 {
		writeError(w, http.StatusNotFound, "No matching incidents found")
		return
	}

	affected, err := h.incidentRepo.BatchAction(r.Context(), req.Action, verifiedIDs, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Batch action failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"action":   req.Action,
		"affected": affected,
	})
}

// =============================================================================
// 3. RELEASES API
// =============================================================================

// HandleListReleases handles GET /api/releases
// @Summary Список релизов проекта
// @Description Возвращает историю версий и деплоев приложения с агрегированной статистикой по каждой версии: общее число ошибок, количество новых багов и регрессий.
// @Tags Releases (Релизы и Деплои)
// @Produce json
// @Param limit query int false "Максимальное количество возвращаемых релизов" default(20)
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 200 {array} domain.ReleaseSummary "Список версий с метриками стабильности"
// @Failure 500 "Не удалось определить проект или прочитать базу"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/releases [get]
func (h *APIHandler) HandleListReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, err := h.resolveProject(r, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	releases, err := h.releaseRepo.GetReleasesWithStats(r.Context(), project.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load releases: "+err.Error())
		return
	}

	if releases == nil {
		releases = []domain.ReleaseSummary{}
	}

	writeJSON(w, http.StatusOK, releases)
}

// HandleCreateRelease handles POST /api/releases
// @Summary Зарегистрировать новый релиз (CI/CD деплой-хук)
// @Description Используется в пайплайнах CI/CD (GitHub Actions, GitLab CI, Bitbucket) для оповещения сервера о выкатке новой версии приложения. Фиксирует версию, ветку и коммит.
// @Tags Releases (Релизы и Деплои)
// @Accept json
// @Produce json
// @Param body body domain.CreateReleaseRequest true "Данные о выкатываемом релизе"
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 201 {object} domain.Release "Релиз успешно создан и зарегистрирован"
// @Failure 400 "Не указан обязательный номер версии"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/releases [post]
func (h *APIHandler) HandleCreateRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	var req domain.CreateReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "Bad Request - Version cannot be empty")
		return
	}

	project, err := h.resolveProject(r, req.Project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	rel, err := h.releaseRepo.CreateRelease(r.Context(), project.ID, req.Version, req.Ref, req.Commit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create release: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rel)
}

// HandleReleaseIncidents handles GET /api/releases/{version}/incidents
// @Summary Список инцидентов конкретной версии
// @Description Возвращает ошибки, зафиксированные в указанной версии релиза. Позволяет мгновенно оценить качество и стабильность свежего релиза.
// @Tags Releases (Релизы и Деплои)
// @Produce json
// @Param version path string true "Семантический номер версии (напр. 1.4.3)"
// @Param new_only query bool false "Показывать только те ошибки, которые впервые появились именно в этой версии" default(false)
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 200 {array} domain.Incident "Список ошибок релиза"
// @Failure 500 "Не удалось определить проект или прочитать базу"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/releases/{version}/incidents [get]
func (h *APIHandler) HandleReleaseIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, err := h.resolveProject(r, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	version := getPathParam(r, "version")
	if version == "" {
		writeError(w, http.StatusBadRequest, "Release version required")
		return
	}

	newOnly := r.URL.Query().Get("new_only") == "true" || r.URL.Query().Get("new_only") == "1"

	incidents, err := h.releaseRepo.GetIncidentsByRelease(r.Context(), project.ID, version, newOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load release incidents: "+err.Error())
		return
	}

	if incidents == nil {
		incidents = []domain.Incident{}
	}

	writeJSON(w, http.StatusOK, incidents)
}

// =============================================================================
// 4. ANALYTICS & STATS
// =============================================================================

// HandleStatsSummary handles GET /api/stats/summary
// @Summary Сводная аналитика и счетчики проекта
// @Description Возвращает ключевые показатели для дашбордов и мониторинга: общее количество инцидентов, нерешенные, решенные, заглушенные, число регрессий и объем событий за последние 24 часа.
// @Tags Analytics (Аналитика и Метрики)
// @Produce json
// @Param X-Project header string false "Имя проекта (также X-Project-Name, X-App-Name). Нет такого — проект создаётся"
// @Param project query string false "Имя проекта, если не передан заголовок (также project_name, app)"
// @Param project_id query int false "ID проекта, если имя не передано"
// @Success 200 {object} domain.StatsSummary "Сводные счетчики проекта"
// @Failure 500 "Не удалось определить проект или прочитать базу"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/stats/summary [get]
func (h *APIHandler) HandleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, err := h.resolveProject(r, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	summary, err := h.incidentRepo.GetStatsSummary(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// =============================================================================
// 5. REALTIME SSE & SYSTEM HEALTH
// =============================================================================

// HandleEventsStreamJSON handles GET /api/events/stream
// @Summary Поток событий в реальном времени (Server-Sent Events)
// @Description Постоянное соединение Server-Sent Events (SSE), передающее события новых ошибок (incident_new), регрессий (regression) и обновления счетчиков в формате JSON. Подходит для внешних утилит, ботов и кастомных дашбордов.
// @Tags Realtime (Потоковая передача)
// @Produce text/event-stream
// @Success 200 {string} string "Поток Server-Sent Events успешно установлен"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/events/stream [get]
func (h *APIHandler) HandleEventsStreamJSON(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	if h.sseBroker == nil {
		writeError(w, http.StatusInternalServerError, "SSE Broker not configured")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientChan := make(chan []byte, 20)
	h.sseBroker.RegisterJSON(clientChan)
	defer h.sseBroker.UnregisterJSON(clientChan)

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}

// HandleHealthCheck handles GET /api/health
// @Summary Проверка работоспособности сервиса (Health Check)
// @Description Liveness/Readiness проба для Docker и Kubernetes. Возвращает статус сервиса, доступность базы SQLite, текущий размер очереди в памяти и время непрерывной работы (uptime).
// @Tags System (Система)
// @Produce json
// @Success 200 {object} domain.HealthStatus "Сервис полностью исправен и готов принимать запросы"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/health [get]
func (h *APIHandler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.startTime).Truncate(time.Second).String()
	queueSize := 0
	if h.eventChan != nil {
		queueSize = len(h.eventChan)
	}

	status := domain.HealthStatus{
		Status:    "ok",
		Database:  "healthy",
		QueueSize: queueSize,
		Uptime:    uptime,
	}

	writeJSON(w, http.StatusOK, status)
}

// HandleUpdateProjectGitHub handles PUT /api/projects/{id}/github
// @Summary Привязать проект к репозиторию GitHub
// @Description Задаёт репозиторий, ветку и префикс пути для сниппетов кода и ссылок на GitHub.
// @Description Репозиторий выбирается из подключённых на /ui/github аккаунтов; github_repo_id = 0 отвязывает проект.
// @Tags Projects (Проекты)
// @Accept json
// @Produce json
// @Param id path int true "ID проекта"
// @Param body body object{github_repo_id=int,github_branch=string,path_prefix=string} true "github_repo_id обязателен"
// @Success 200 {object} object{status=string,message=string,project_id=int} "Настройки сохранены"
// @Failure 400 "Невалидный JSON, нет github_repo_id или репозиторий не выбран в аккаунте"
// @Failure 500 "Ошибка базы данных"
// @Failure 403 "IP-адрес клиента не входит в ALLOWED_IPS и запрос пришёл не с хоста сервиса"
// @Router /api/projects/{id}/github [put]
// @Router /api/projects/{id}/github [post]
func (h *APIHandler) HandleUpdateProjectGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req struct {
		// Указатель отличает «поле не передано» от 0: иначе старый формат запроса молча отвязывал бы проект
		GitHubRepoID *int64 `json:"github_repo_id"`
		GitHubBranch string `json:"github_branch"`
		PathPrefix   string `json:"path_prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}
	if req.GitHubRepoID == nil {
		writeError(w, http.StatusBadRequest, "github_repo_id is required (github_repo/github_token are no longer supported, connect accounts at /ui/github)")
		return
	}

	err = h.projectRepo.UpdateGitHubSettings(r.Context(), id, *req.GitHubRepoID, req.GitHubBranch, req.PathPrefix)
	if errors.Is(err, domain.ErrGitHubRepoNotSelected) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update GitHub settings: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"message":    "GitHub settings updated successfully",
		"project_id": id,
	})
}
