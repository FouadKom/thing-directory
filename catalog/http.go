// Copyright 2014-2016 Fraunhofer Institute for Applied Information Technology FIT

package catalog

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/linksmart/service-catalog/v3/utils"
	"github.com/linksmart/thing-directory/wot"
)

const (
	MaxPerPage = 100
	// query parameters
	QueryParamPage     = "page"
	QueryParamPerPage  = "per_page"
	QueryParamJSONPath = "jsonpath"
	QueryParamXPath    = "xpath"
	// Deprecated
	QueryParamFetchPath = "fetch"
)

type ThingDescriptionPage struct {
	Context string      `json:"@context"`
	Type    string      `json:"@type"`
	Items   interface{} `json:"items"`
	Page    int         `json:"page"`
	PerPage int         `json:"perPage"`
	Total   int         `json:"total"`
}

type ValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

type HTTPAPI struct {
	controller  CatalogController
	contentType string
}

func NewHTTPAPI(controller CatalogController, version string) *HTTPAPI {
	contentType := ResponseMediaType
	if version != "" {
		contentType += ";version=" + version
	}
	return &HTTPAPI{
		controller:  controller,
		contentType: contentType,
	}
}

// Post handler creates one item
func (a *HTTPAPI) Post(w http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		ErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	var td ThingDescription
	if err := json.Unmarshal(body, &td); err != nil {
		ErrorResponse(w, http.StatusBadRequest, "Error processing the request:", err.Error())
		return
	}

	if td[_id] != nil {
		id, ok := td[_id].(string)
		if !ok || id != "" {
			ErrorResponse(w, http.StatusBadRequest, "Registering with user-defined id is not possible using a POST request.")
			return
		}
	}

	id, err := a.controller.add(td)
	if err != nil {
		switch err.(type) {
		case *ConflictError:
			ErrorResponse(w, http.StatusConflict, "Error creating the resource:", err.Error())
			return
		case *BadRequestError:
			ErrorResponse(w, http.StatusBadRequest, "Invalid registration:", err.Error())
			return
		default:
			ErrorResponse(w, http.StatusInternalServerError, "Error creating the registration:", err.Error())
			return
		}
	}

	w.Header().Set("Location", id)
	w.WriteHeader(http.StatusCreated)
}

// Put handler updates an existing item (Response: StatusOK)
// If the item does not exist, a new one will be created with the given id (Response: StatusCreated)
func (a *HTTPAPI) Put(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)

	body, err := ioutil.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		ErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	var td ThingDescription
	if err := json.Unmarshal(body, &td); err != nil {
		ErrorResponse(w, http.StatusBadRequest, "Error processing the request:", err.Error())
		return
	}

	if id, ok := td[_id].(string); !ok || id == "" {
		ErrorResponse(w, http.StatusBadRequest, "Registration without id is not possible using a PUT request.")
		return
	}
	if params["id"] != td[_id] {
		ErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Resource id in path (%s) does not match the id in body (%s)", params["id"], td[_id]))
		return
	}

	err = a.controller.update(params["id"], td)
	if err != nil {
		switch err.(type) {
		case *NotFoundError:
			// Create a new device with the given id
			id, err := a.controller.add(td)
			if err != nil {
				switch err.(type) {
				case *ConflictError:
					ErrorResponse(w, http.StatusConflict, "Error creating the registration:", err.Error())
					return
				case *BadRequestError:
					ErrorResponse(w, http.StatusBadRequest, "Invalid registration:", err.Error())
					return
				default:
					ErrorResponse(w, http.StatusInternalServerError, "Error creating the registration:", err.Error())
					return
				}
			}
			w.Header().Set("Content-Type", a.contentType)
			w.Header().Set("Location", id)
			w.WriteHeader(http.StatusCreated)
			return
		case *BadRequestError:
			ErrorResponse(w, http.StatusBadRequest, "Invalid registration:", err.Error())
			return
		default:
			ErrorResponse(w, http.StatusInternalServerError, "Error updating the registration:", err.Error())
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// Patch updates parts or all of an existing item (Response: StatusOK)
func (a *HTTPAPI) Patch(w http.ResponseWriter, req *http.Request) {
	ErrorResponse(w, http.StatusNotImplemented, "PATCH method is not implemented")
}

// Get handler get one item
func (a *HTTPAPI) Get(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)

	td, err := a.controller.get(params["id"])
	if err != nil {
		switch err.(type) {
		case *NotFoundError:
			ErrorResponse(w, http.StatusNotFound, err.Error())
			return
		default:
			ErrorResponse(w, http.StatusInternalServerError, "Error retrieving the registration:", err.Error())
			return
		}
	}

	b, err := json.Marshal(td)
	if err != nil {
		ErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	w.Write(b)
}

// Delete removes one item
func (a *HTTPAPI) Delete(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)

	err := a.controller.delete(params["id"])
	if err != nil {
		switch err.(type) {
		case *NotFoundError:
			ErrorResponse(w, http.StatusNotFound, err.Error())
			return
		default:
			ErrorResponse(w, http.StatusInternalServerError, "Error deleting the registration:", err.Error())
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// GetMany lists entries in a paginated catalog format
func (a *HTTPAPI) GetMany(w http.ResponseWriter, req *http.Request) {
	err := req.ParseForm()
	if err != nil {
		ErrorResponse(w, http.StatusBadRequest, "Error parsing the query:", err.Error())
		return
	}
	page, perPage, err := utils.ParsePagingParams(
		req.Form.Get(QueryParamPage), req.Form.Get(QueryParamPerPage), MaxPerPage)
	if err != nil {
		ErrorResponse(w, http.StatusBadRequest, "Error parsing query parameters:", err.Error())
		return
	}

	var items interface{}
	var total int
	if jsonPath := req.Form.Get(QueryParamJSONPath); jsonPath != "" {
		if req.Form.Get(QueryParamXPath) != "" {
			ErrorResponse(w, http.StatusBadRequest, "query with jsonpath should not be mixed with xpath")
			return
		}
		w.Header().Add("X-Request-Jsonpath", jsonPath)
		items, total, err = a.controller.filterJSONPath(jsonPath, page, perPage)
		if err != nil {
			switch err.(type) {
			case *BadRequestError:
				ErrorResponse(w, http.StatusBadRequest, err.Error())
				return
			default:
				ErrorResponse(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	} else if xPath := req.Form.Get(QueryParamXPath); xPath != "" {
		w.Header().Add("X-Request-Xpath", xPath)
		items, total, err = a.controller.filterXPath(xPath, page, perPage)
		if err != nil {
			switch err.(type) {
			case *BadRequestError:
				ErrorResponse(w, http.StatusBadRequest, err.Error())
				return
			default:
				ErrorResponse(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	} else if req.Form.Get(QueryParamFetchPath) != "" {
		ErrorResponse(w, http.StatusBadRequest, "fetch query parameter is deprecated. Use jsonpath or xpath")
		return
	} else {
		items, total, err = a.controller.list(page, perPage)
		if err != nil {
			switch err.(type) {
			case *BadRequestError:
				ErrorResponse(w, http.StatusBadRequest, err.Error())
				return
			default:
				ErrorResponse(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}

	coll := &ThingDescriptionPage{
		Context: ResponseContextURL,
		Type:    ResponseType,
		Items:   items,
		Page:    page,
		PerPage: perPage,
		Total:   total,
	}

	b, err := json.Marshal(coll)
	if err != nil {
		ErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("X-Request-URL", req.RequestURI)
	w.Write(b)
}

// GetValidation handler gets validation for the request body
func (a *HTTPAPI) GetValidation(w http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		ErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(body) == 0 {
		ErrorResponse(w, http.StatusBadRequest, "Empty request body")
		return
	}

	var td ThingDescription
	if err := json.Unmarshal(body, &td); err != nil {
		ErrorResponse(w, http.StatusBadRequest, "Error processing the request:", err.Error())
		return
	}

	var response ValidationResult
	if err := validateThingDescription(td); err != nil {
		if verr, ok := err.(*wot.ValidationError); ok {
			response.Errors = verr.Errors
		} else {
			response.Errors = []string{err.Error()}
		}
	} else {
		response.Valid = true
	}

	b, err := json.Marshal(response)
	if err != nil {
		ErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
