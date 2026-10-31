import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/delivery-promise-api",
    },
    {
      type: "category",
      label: "DeliveryOffers",
      link: {
        type: "doc",
        id: "api-reference/rest/delivery-offers",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/quote-delivery-offers",
          label: "Quote delivery offers per tier for a destination and a set of lines",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-delivery-offer",
          label: "Get one delivery offer",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Promises",
      link: {
        type: "doc",
        id: "api-reference/rest/promises",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/make-promise",
          label: "Make a provisional promise for one shipment group from a quoted offer",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-promises-by-order",
          label: "List the promises of one customer order",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-promise",
          label: "Get one promise with its terms, witness path, revisions and judgement",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/confirm-promise",
          label: "Confirm a provisional promise for a placed order (ConfirmPromise)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-promise",
          label: "Cancel a confirmed promise on request (CancelPromise)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "PromiseBooks",
      link: {
        type: "doc",
        id: "api-reference/rest/promise-books",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-promise-books",
          label: "List the promise books of one node for one date",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "PromisePolicy",
      link: {
        type: "doc",
        id: "api-reference/rest/promise-policy",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/publish-promise-policy",
          label: "Publish a new promise policy version (PublishPromisePolicy)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-current-promise-policy",
          label: "Get the current promise policy",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
